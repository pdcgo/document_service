package document_service

import (
	"net/http"

	"cloud.google.com/go/storage"
	"connectrpc.com/connect"
	"github.com/pdcgo/document_service/document"
	"github.com/pdcgo/san_collection/san_caches"
	"github.com/pdcgo/schema/services/document_iface/v1/document_ifaceconnect"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/custom_connect"
	"github.com/pdcgo/user_service/access_interceptors"
	"gorm.io/gorm"
)

type ServiceReflectNames []string
type RegisterHandler func() ServiceReflectNames

// NewRegister builds the keyless GCS signer + object store and returns a handler that
// mounts the DocumentService on mux. The access interceptor enforces each request's
// (role_base.v1.request_policy) and injects the caller identity into context
// (mirrors invoice_service/register.go).
func NewRegister(
	mux *http.ServeMux,
	db *gorm.DB,
	cfg *configs.AppConfig,
	docCfg document.Config,
	cacheMgr san_caches.CacheManager,
	defaultInterceptor custom_connect.DefaultInterceptor,
	storageClient *storage.Client,
) (RegisterHandler, error) {
	signer, err := document.NewSigner()
	if err != nil {
		return nil, err
	}
	store := document.NewStore(storageClient)

	return func() ServiceReflectNames {
		roleOpt := connect.WithInterceptors(access_interceptors.NewAccessInterceptor(db, cfg.JwtSecret, cacheMgr))
		path, handler := document_ifaceconnect.NewDocumentServiceHandler(
			document.NewDocumentService(db, signer, store, docCfg),
			defaultInterceptor,
			roleOpt,
		)
		mux.Handle(path, handler)
		return ServiceReflectNames{document_ifaceconnect.DocumentServiceName}
	}, nil
}
