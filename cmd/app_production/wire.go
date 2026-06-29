//go:build wireinject
// +build wireinject

package main

import (
	"net/http"

	"github.com/google/wire"
	document_service "github.com/pdcgo/document_service"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/custom_connect"
	"github.com/urfave/cli/v3"
)

func InitializeApp() (*cli.Command, error) {
	wire.Build(
		configs.NewProductionConfig,
		http.NewServeMux,
		NewStorageClient,
		custom_connect.NewRegisterReflect,
		custom_connect.NewDefaultInterceptor,
		NewCacheManager,
		NewDatabase,
		NewDocumentConfig,
		document_service.NewRegister,
		NewServiceApiFunc,
		NewApp,
	)
	return &cli.Command{}, nil
}
