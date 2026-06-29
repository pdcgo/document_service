// Package document implements the document_iface.v1 DocumentService (V4 signed-URL
// direct-to-GCS uploads, media-library model). Authorization is enforced by the access
// interceptor (proto request_policy), not in these handlers. GCS signing/object ops are
// injected behind the Signer/ObjectStore interfaces so the handler logic is unit-testable.
package document

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	document_iface "github.com/pdcgo/schema/services/document_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
)

// Signer issues short-lived V4 signed URLs for direct-to-GCS upload/download.
type Signer interface {
	SignedPutURL(ctx context.Context, bucket, objectKey, contentType string, ttl time.Duration) (string, error)
	SignedGetURL(ctx context.Context, bucket, objectKey string, ttl time.Duration) (string, error)
}

// ObjectStore is the subset of GCS object operations the service needs.
type ObjectStore interface {
	Stat(ctx context.Context, bucket, objectKey string) (size int64, contentType string, err error)
	Move(ctx context.Context, bucket, srcKey, dstKey string) error
	Delete(ctx context.Context, bucket, objectKey string) error
	// SetPublic grants AllUsers read on the object (for public resource types).
	SetPublic(ctx context.Context, bucket, objectKey string) error
}

type Config struct {
	Bucket         string
	IncomingPrefix string // unconfirmed uploads land here (lifecycle-TTL reaped)
	AssetPrefix    string // confirmed assets are moved here (permanent)
	TokenSecret    []byte // HMAC secret for the upload token
	URLTTL         time.Duration
	MaxSizeBytes   int64
	PublicBaseURL  string // base for stable public URLs (e.g. https://storage.googleapis.com or a CDN)
}

func (c Config) withDefaults() Config {
	if c.IncomingPrefix == "" {
		c.IncomingPrefix = "incoming"
	}
	if c.AssetPrefix == "" {
		c.AssetPrefix = "assets"
	}
	if c.URLTTL == 0 {
		c.URLTTL = 15 * time.Minute
	}
	if c.MaxSizeBytes == 0 {
		c.MaxSizeBytes = 10 << 20 // 10 MiB
	}
	if c.PublicBaseURL == "" {
		c.PublicBaseURL = "https://storage.googleapis.com"
	}
	return c
}

// publicResources are the resource types whose confirmed objects are made world-readable
// and served via a stable, non-expiring public URL. All other types stay private (signed URLs).
var publicResources = map[db_models.ResourceType]bool{
	db_models.ProductResource:        true,
	db_models.ProfilePictureResource: true,
	db_models.WarehouseResource:      true,
}

func isPublicResource(rt db_models.ResourceType) bool {
	return publicResources[rt]
}

// content-type -> file extension for keys.
var extByMime = map[string]string{
	"image/jpeg":      "jpg",
	"image/jpg":       "jpg",
	"image/png":       "png",
	"image/webp":      "webp",
	"application/pdf": "pdf",
}

var imageMimes = []string{"image/jpeg", "image/jpg", "image/png", "image/webp"}

// Allowed content types per resource type (mirrors asset_service's per-type rules).
var allowedByType = map[document_iface.DocumentResourceType][]string{
	document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PRODUCT:         imageMimes,
	document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PROFILE_PICTURE: imageMimes,
	document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_WAREHOUSE:       imageMimes,
	document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_TRANSACTION:     append([]string{"application/pdf"}, imageMimes...),
	document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_INVOICE:         append([]string{"application/pdf"}, imageMimes...),
	document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_GENERAL:         append([]string{"application/pdf"}, imageMimes...),
}

func contentTypeAllowed(rt document_iface.DocumentResourceType, ct string) bool {
	for _, a := range allowedByType[rt] {
		if a == ct {
			return true
		}
	}
	return false
}

func resourceSlug(rt document_iface.DocumentResourceType) (string, db_models.ResourceType) {
	switch rt {
	case document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PRODUCT:
		return "product", db_models.ProductResource
	case document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_TRANSACTION:
		return "transaction", db_models.TransactionResource
	case document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_PROFILE_PICTURE:
		return "profile_picture", db_models.ProfilePictureResource
	case document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_WAREHOUSE:
		return "warehouse", db_models.WarehouseResource
	case document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_INVOICE:
		return "invoice", db_models.InvoiceResource
	default:
		return "general", db_models.ResourceType("general_resources")
	}
}

type documentServiceImpl struct {
	db     *gorm.DB
	signer Signer
	store  ObjectStore
	cfg    Config
}

func NewDocumentService(
	db *gorm.DB,
	signer Signer,
	store ObjectStore,
	cfg Config,
) *documentServiceImpl {
	return &documentServiceImpl{
		db:     db,
		signer: signer,
		store:  store,
		cfg:    cfg.withDefaults(),
	}
}

// ---- upload token (HMAC of documentID + expiry) -----------------------------

func makeToken(id string, exp time.Time, secret []byte) string {
	payload := id + ":" + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

func verifyToken(token string, secret []byte) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed token")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("malformed token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payloadBytes)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", fmt.Errorf("invalid token signature")
	}
	payload := string(payloadBytes)
	idx := strings.LastIndex(payload, ":")
	if idx < 0 {
		return "", fmt.Errorf("malformed token payload")
	}
	exp, err := strconv.ParseInt(payload[idx+1:], 10, 64)
	if err != nil {
		return "", fmt.Errorf("malformed token expiry")
	}
	if time.Now().Unix() > exp {
		return "", fmt.Errorf("token expired")
	}
	return payload[:idx], nil
}
