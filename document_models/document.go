package document_models

import (
	"time"

	"github.com/pdcgo/shared/db_models"
)

type DocumentStatus string

const (
	// DocumentPending: RequestUpload issued, object not yet confirmed (lives under the
	// incoming/ prefix and is reaped by the bucket lifecycle TTL if never confirmed).
	DocumentPending DocumentStatus = "pending"
	// DocumentActive: confirmed, moved to the permanent prefix, a usable team asset.
	DocumentActive DocumentStatus = "active"
)

// Document is a team-owned uploaded asset (media-library model): business entities
// reference it by ID. Stored in a private GCS bucket; served via short-lived signed URLs.
// The struct name maps to table "documents" by GORM default naming.
type Document struct {
	ID           string                 `json:"id" gorm:"primaryKey;type:varchar(64)"`
	TeamID       uint                   `json:"team_id" gorm:"index"`
	ResourceType db_models.ResourceType `json:"resource_type"`
	BucketName   string                 `json:"bucket_name"`
	ObjectKey    string                 `json:"object_key" gorm:"index"`
	MimeType     string                 `json:"mime_type"`
	Size         int64                  `json:"size"`
	OriginalName string                 `json:"original_name"`
	CreatedByID  uint                   `json:"created_by_id"`
	Status       DocumentStatus         `json:"status"`
	// PublicURL is the stable, non-expiring URL for public resource types (object made
	// world-readable on confirm). Empty for private types, which are served via signed URLs.
	PublicURL string    `json:"public_url" gorm:"type:varchar(1024)"`
	CreatedAt time.Time `json:"created_at"`
}

// GetEntityID implements the authorization Entity interface so a Document can be the
// subject of a team-scoped permission check.
func (Document) GetEntityID() string {
	return "document"
}
