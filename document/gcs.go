package document

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/compute/metadata"
	credentials "cloud.google.com/go/iam/credentials/apiv1"
	"cloud.google.com/go/iam/credentials/apiv1/credentialspb"
	"cloud.google.com/go/storage"
)

// gcsSigner implements Signer with V4 signed URLs signed KEYLESSLY via the IAM
// Credentials SignBlob API — required on Cloud Run where the service account has no
// exported private key. The runtime SA needs roles/iam.serviceAccountTokenCreator on itself.
type gcsSigner struct {
	saEmail string
	iam     *credentials.IamCredentialsClient
}

func (s *gcsSigner) sign(method, bucket, key, contentType string, ttl time.Duration) (string, error) {
	opts := &storage.SignedURLOptions{
		Scheme:         storage.SigningSchemeV4,
		Method:         method,
		Expires:        time.Now().Add(ttl),
		GoogleAccessID: s.saEmail,
		SignBytes: func(b []byte) ([]byte, error) {
			resp, err := s.iam.SignBlob(context.Background(), &credentialspb.SignBlobRequest{
				Name:    "projects/-/serviceAccounts/" + s.saEmail,
				Payload: b,
			})
			if err != nil {
				return nil, err
			}
			return resp.SignedBlob, nil
		},
	}
	if contentType != "" {
		opts.ContentType = contentType
	}
	return storage.SignedURL(bucket, key, opts)
}

func (s *gcsSigner) SignedPutURL(_ context.Context, bucket, key, contentType string, ttl time.Duration) (string, error) {
	return s.sign(http.MethodPut, bucket, key, contentType, ttl)
}

func (s *gcsSigner) SignedGetURL(_ context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return s.sign(http.MethodGet, bucket, key, "", ttl)
}

// NewSigner builds the keyless IAM-backed Signer (SA email from the metadata server,
// or the SIGNER_SA_EMAIL env for local/dev).
func NewSigner() (Signer, error) {
	ctx := context.Background()
	email, err := metadata.EmailWithContext(ctx, "default")
	if err != nil || email == "" {
		email = os.Getenv("SIGNER_SA_EMAIL")
	}
	iamClient, err := credentials.NewIamCredentialsClient(ctx)
	if err != nil {
		return nil, err
	}
	return &gcsSigner{saEmail: email, iam: iamClient}, nil
}

// gcsObjectStore implements ObjectStore over a *storage.Client.
type gcsObjectStore struct{ client *storage.Client }

func NewStore(client *storage.Client) ObjectStore {
	return &gcsObjectStore{client: client}
}

func (s *gcsObjectStore) Stat(ctx context.Context, bucket, key string) (int64, string, error) {
	attrs, err := s.client.Bucket(bucket).Object(key).Attrs(ctx)
	if err != nil {
		return 0, "", err
	}
	return attrs.Size, attrs.ContentType, nil
}

func (s *gcsObjectStore) Move(ctx context.Context, bucket, src, dst string) error {
	srcObj := s.client.Bucket(bucket).Object(src)
	dstObj := s.client.Bucket(bucket).Object(dst)
	if _, err := dstObj.CopierFrom(srcObj).Run(ctx); err != nil {
		return err
	}
	return srcObj.Delete(ctx)
}

func (s *gcsObjectStore) Delete(ctx context.Context, bucket, key string) error {
	err := s.client.Bucket(bucket).Object(key).Delete(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	return err
}

// SetPublic makes the object world-readable (mirrors the legacy wd_storage.SetPublic).
// Requires the bucket to use fine-grained ACLs (not Uniform Bucket-Level Access).
func (s *gcsObjectStore) SetPublic(ctx context.Context, bucket, key string) error {
	return s.client.Bucket(bucket).Object(key).ACL().Set(ctx, storage.AllUsers, storage.RoleReader)
}
