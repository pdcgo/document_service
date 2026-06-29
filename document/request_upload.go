package document

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/pdcgo/document_service/document_models"
	document_iface "github.com/pdcgo/schema/services/document_iface/v1"
	"github.com/pdcgo/user_service/access_interceptors"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RequestUpload implements [document_ifaceconnect.DocumentServiceHandler].
func (d *documentServiceImpl) RequestUpload(
	ctx context.Context,
	req *connect.Request[document_iface.RequestUploadRequest],
) (*connect.Response[document_iface.RequestUploadResponse], error) {
	pay := req.Msg
	db := d.db.WithContext(ctx)

	if pay.ResourceType == document_iface.DocumentResourceType_DOCUMENT_RESOURCE_TYPE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("resource_type required"))
	}
	if !contentTypeAllowed(pay.ResourceType, pay.ContentType) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("content_type %q not allowed for this resource", pay.ContentType))
	}
	if pay.SizeBytes > d.cfg.MaxSizeBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("file too large (max %d bytes)", d.cfg.MaxSizeBytes))
	}

	slug, dbType := resourceSlug(pay.ResourceType)
	id := uuid.NewString()
	ext := extByMime[pay.ContentType]
	key := fmt.Sprintf("%s/teams/%d/%s/%s.%s", d.cfg.IncomingPrefix, pay.TeamId, slug, id, ext)

	doc := document_models.Document{
		ID:           id,
		TeamID:       uint(pay.TeamId),
		ResourceType: dbType,
		BucketName:   d.cfg.Bucket,
		ObjectKey:    key,
		MimeType:     pay.ContentType,
		Size:         pay.SizeBytes,
		OriginalName: pay.Filename,
		Status:       document_models.DocumentPending,
		CreatedAt:    time.Now(),
	}
	// Best-effort audit: the access interceptor already authorized the call and put
	// the caller identity in ctx.
	if caller, err := access_interceptors.GetIdentityFromCtx(ctx); err == nil {
		doc.CreatedByID = uint(caller.IdentityId)
	}
	if err := db.Create(&doc).Error; err != nil {
		return nil, err
	}

	exp := time.Now().Add(d.cfg.URLTTL)
	url, err := d.signer.SignedPutURL(ctx, d.cfg.Bucket, key, pay.ContentType, d.cfg.URLTTL)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&document_iface.RequestUploadResponse{
		UploadUrl:   url,
		Method:      "PUT",
		Headers:     map[string]string{"Content-Type": pay.ContentType},
		UploadToken: makeToken(id, exp, d.cfg.TokenSecret),
		ExpiresAt:   timestamppb.New(exp),
	}), nil
}
