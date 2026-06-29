package document

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/pdcgo/document_service/document_models"
	document_iface "github.com/pdcgo/schema/services/document_iface/v1"
)

// ConfirmUpload implements [document_ifaceconnect.DocumentServiceHandler].
func (d *documentServiceImpl) ConfirmUpload(
	ctx context.Context,
	req *connect.Request[document_iface.ConfirmUploadRequest],
) (*connect.Response[document_iface.ConfirmUploadResponse], error) {
	id, err := verifyToken(req.Msg.UploadToken, d.cfg.TokenSecret)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	db := d.db.WithContext(ctx)

	var doc document_models.Document
	if err := db.
		Where("id = ? AND status = ?", id, document_models.DocumentPending).
		First(&doc).Error; err != nil {
		return nil, err
	}

	size, ctype, err := d.store.Stat(ctx, doc.BucketName, doc.ObjectKey)
	if err != nil {
		return nil, err
	}
	// Reject (and delete) an object that violates the declared constraints.
	if size > d.cfg.MaxSizeBytes || (ctype != "" && ctype != doc.MimeType) {
		_ = d.store.Delete(ctx, doc.BucketName, doc.ObjectKey)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("uploaded object failed validation"))
	}

	// Move out of the incoming/ prefix so the lifecycle TTL never reaps a confirmed asset.
	assetKey := d.cfg.AssetPrefix + strings.TrimPrefix(doc.ObjectKey, d.cfg.IncomingPrefix)
	if err := d.store.Move(ctx, doc.BucketName, doc.ObjectKey, assetKey); err != nil {
		return nil, err
	}

	updates := map[string]interface{}{
		"object_key": assetKey,
		"size":       size,
		"status":     document_models.DocumentActive,
	}

	// Public resource types (product/profile_picture/warehouse) get a world-readable object
	// and a stable, non-expiring URL; private types keep signed-URL-only access.
	var publicURL string
	if isPublicResource(doc.ResourceType) {
		if err := d.store.SetPublic(ctx, doc.BucketName, assetKey); err != nil {
			return nil, err
		}
		publicURL = fmt.Sprintf("%s/%s/%s", d.cfg.PublicBaseURL, doc.BucketName, assetKey)
		updates["public_url"] = publicURL
	}

	if err := db.Model(&doc).Updates(updates).Error; err != nil {
		return nil, err
	}

	return connect.NewResponse(&document_iface.ConfirmUploadResponse{
		DocumentId: id,
		PublicUrl:  publicURL,
	}), nil
}
