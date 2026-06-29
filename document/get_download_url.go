package document

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/document_service/document_models"
	document_iface "github.com/pdcgo/schema/services/document_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetDownloadUrl implements [document_ifaceconnect.DocumentServiceHandler].
func (d *documentServiceImpl) GetDownloadUrl(
	ctx context.Context,
	req *connect.Request[document_iface.GetDownloadUrlRequest],
) (*connect.Response[document_iface.GetDownloadUrlResponse], error) {
	db := d.db.WithContext(ctx)

	var doc document_models.Document
	if err := db.
		Where("id = ? AND status = ?", req.Msg.DocumentId, document_models.DocumentActive).
		First(&doc).Error; err != nil {
		return nil, err
	}

	// Public assets have a stable, non-expiring URL — return it directly, no signing.
	if doc.PublicURL != "" {
		return connect.NewResponse(&document_iface.GetDownloadUrlResponse{
			Url:    doc.PublicURL,
			Public: true,
		}), nil
	}

	exp := time.Now().Add(d.cfg.URLTTL)
	url, err := d.signer.SignedGetURL(ctx, doc.BucketName, doc.ObjectKey, d.cfg.URLTTL)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&document_iface.GetDownloadUrlResponse{
		Url:       url,
		ExpiresAt: timestamppb.New(exp),
	}), nil
}
