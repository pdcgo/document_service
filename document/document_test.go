package document

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/document_service/document_models"
	document_iface "github.com/pdcgo/schema/services/document_iface/v1"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// --- mocks (GCS only; authz is the interceptor's job, not these handlers) ----

type mockSigner struct{}

func (mockSigner) SignedPutURL(_ context.Context, bucket, key, _ string, _ time.Duration) (string, error) {
	return "https://signed.put/" + bucket + "/" + key, nil
}
func (mockSigner) SignedGetURL(_ context.Context, bucket, key string, _ time.Duration) (string, error) {
	return "https://signed.get/" + bucket + "/" + key, nil
}

type mockStore struct {
	statSize  int64
	statCType string
	moved     [][2]string
	deleted   []string
	publicSet []string
}

func (s *mockStore) Stat(_ context.Context, _, _ string) (int64, string, error) {
	return s.statSize, s.statCType, nil
}
func (s *mockStore) Move(_ context.Context, _, src, dst string) error {
	s.moved = append(s.moved, [2]string{src, dst})
	return nil
}
func (s *mockStore) Delete(_ context.Context, _, key string) error {
	s.deleted = append(s.deleted, key)
	return nil
}
func (s *mockStore) SetPublic(_ context.Context, _, key string) error {
	s.publicSet = append(s.publicSet, key)
	return nil
}

func newSvc(db *gorm.DB, store *mockStore) *documentServiceImpl {
	return NewDocumentService(db, mockSigner{}, store, Config{
		Bucket:       "test-bucket",
		TokenSecret:  []byte("test-secret"),
		MaxSizeBytes: 1 << 20, // 1 MiB
	})
}

// --- tests -------------------------------------------------------------------

func TestDocumentService(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "document service",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&document_models.Document{}))

				store := &mockStore{statSize: 500, statCType: "image/png"}
				svc := newSvc(db, store)
				ctx := t.Context()

				var token string

				t.Run("RequestUpload happy path writes a pending row + signed url", func(t *testing.T) {
					res, err := svc.RequestUpload(ctx, connect.NewRequest(&document_iface.RequestUploadRequest{
						TeamId:       7,
						ResourceType: document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PRODUCT,
						ContentType:  "image/png",
						SizeBytes:    500,
						Filename:     "photo.png",
					}))
					assert.NoError(t, err)
					assert.Equal(t, "PUT", res.Msg.Method)
					assert.Contains(t, res.Msg.UploadUrl, "incoming/teams/7/product/")
					assert.NotEmpty(t, res.Msg.UploadToken)
					token = res.Msg.UploadToken

					var n int64
					db.Model(&document_models.Document{}).Where("team_id = ? AND status = ?", 7, document_models.DocumentPending).Count(&n)
					assert.Equal(t, int64(1), n)
				})

				t.Run("RequestUpload rejects disallowed content-type", func(t *testing.T) {
					_, err := svc.RequestUpload(ctx, connect.NewRequest(&document_iface.RequestUploadRequest{
						TeamId:       7,
						ResourceType: document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PRODUCT,
						ContentType:  "application/pdf", // not allowed for PRODUCT
						SizeBytes:    500,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("RequestUpload rejects oversize", func(t *testing.T) {
					_, err := svc.RequestUpload(ctx, connect.NewRequest(&document_iface.RequestUploadRequest{
						TeamId:       7,
						ResourceType: document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_INVOICE,
						ContentType:  "application/pdf",
						SizeBytes:    (1 << 20) + 1,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("ConfirmUpload of a public type moves to assets/, makes it public, returns public_url", func(t *testing.T) {
					res, err := svc.ConfirmUpload(ctx, connect.NewRequest(&document_iface.ConfirmUploadRequest{
						UploadToken: token,
					}))
					assert.NoError(t, err)
					assert.NotEmpty(t, res.Msg.DocumentId)
					assert.Len(t, store.moved, 1)
					assert.Contains(t, store.moved[0][1], "assets/teams/7/product/")

					// PRODUCT is a public type: object made world-readable + a stable URL returned.
					assert.Len(t, store.publicSet, 1)
					assert.Contains(t, store.publicSet[0], "assets/teams/7/product/")
					assert.Contains(t, res.Msg.PublicUrl, "https://storage.googleapis.com/test-bucket/assets/teams/7/product/")

					var doc document_models.Document
					assert.NoError(t, db.First(&doc, "id = ?", res.Msg.DocumentId).Error)
					assert.Equal(t, document_models.DocumentActive, doc.Status)
					assert.Contains(t, doc.ObjectKey, "assets/teams/7/product/")
					assert.Equal(t, res.Msg.PublicUrl, doc.PublicURL)
				})

				t.Run("ConfirmUpload rejects a bad token", func(t *testing.T) {
					_, err := svc.ConfirmUpload(ctx, connect.NewRequest(&document_iface.ConfirmUploadRequest{
						UploadToken: "garbage.token",
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("ConfirmUpload deletes + rejects an oversize object", func(t *testing.T) {
					req, _ := svc.RequestUpload(ctx, connect.NewRequest(&document_iface.RequestUploadRequest{
						TeamId:       7,
						ResourceType: document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PRODUCT,
						ContentType:  "image/png",
						SizeBytes:    500,
					}))
					store.statSize = (1 << 20) + 5 // server-side says it's actually too big
					_, err := svc.ConfirmUpload(ctx, connect.NewRequest(&document_iface.ConfirmUploadRequest{
						UploadToken: req.Msg.UploadToken,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
					assert.NotEmpty(t, store.deleted)
					store.statSize = 500 // restore
				})

				t.Run("GetDownloadUrl returns the stable public url for a public doc", func(t *testing.T) {
					ru, _ := svc.RequestUpload(ctx, connect.NewRequest(&document_iface.RequestUploadRequest{
						TeamId:       7,
						ResourceType: document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PRODUCT,
						ContentType:  "image/png",
						SizeBytes:    500,
					}))
					cu, err := svc.ConfirmUpload(ctx, connect.NewRequest(&document_iface.ConfirmUploadRequest{UploadToken: ru.Msg.UploadToken}))
					assert.NoError(t, err)

					res, err := svc.GetDownloadUrl(ctx, connect.NewRequest(&document_iface.GetDownloadUrlRequest{DocumentId: cu.Msg.DocumentId}))
					assert.NoError(t, err)
					assert.True(t, res.Msg.Public)
					assert.Nil(t, res.Msg.ExpiresAt)
					assert.Equal(t, cu.Msg.PublicUrl, res.Msg.Url)
					assert.Contains(t, res.Msg.Url, "https://storage.googleapis.com/test-bucket/assets/teams/7/product/")
				})

				t.Run("private type stays signed: no public_url, no SetPublic, signed download with expiry", func(t *testing.T) {
					before := len(store.publicSet)
					ru, err := svc.RequestUpload(ctx, connect.NewRequest(&document_iface.RequestUploadRequest{
						TeamId:       7,
						ResourceType: document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_INVOICE,
						ContentType:  "application/pdf",
						SizeBytes:    500,
						Filename:     "inv.pdf",
					}))
					assert.NoError(t, err)
					store.statCType = "application/pdf" // match the declared mime for confirm validation

					cu, err := svc.ConfirmUpload(ctx, connect.NewRequest(&document_iface.ConfirmUploadRequest{UploadToken: ru.Msg.UploadToken}))
					assert.NoError(t, err)
					assert.Empty(t, cu.Msg.PublicUrl)
					assert.Len(t, store.publicSet, before) // SetPublic NOT called for a private type

					var doc document_models.Document
					assert.NoError(t, db.First(&doc, "id = ?", cu.Msg.DocumentId).Error)
					assert.Empty(t, doc.PublicURL)
					assert.Contains(t, doc.ObjectKey, "assets/teams/7/invoice/")

					res, err := svc.GetDownloadUrl(ctx, connect.NewRequest(&document_iface.GetDownloadUrlRequest{DocumentId: cu.Msg.DocumentId}))
					assert.NoError(t, err)
					assert.False(t, res.Msg.Public)
					assert.NotNil(t, res.Msg.ExpiresAt)
					assert.Contains(t, res.Msg.Url, "https://signed.get/test-bucket/assets/teams/7/invoice/")

					store.statCType = "image/png" // restore for any later cases
				})
			})
		},
	)
}
