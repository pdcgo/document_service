package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	document_service "github.com/pdcgo/document_service"
	"github.com/pdcgo/shared/custom_connect"
	"github.com/urfave/cli/v3"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ServiceApiFunc cli.ActionFunc

func NewServiceApiFunc(
	mux *http.ServeMux,
	docRegister document_service.RegisterHandler,
	reflectorRegister custom_connect.RegisterReflectFunc,
) ServiceApiFunc {
	return func(ctx context.Context, c *cli.Command) error {
		cancel, err := custom_connect.InitTracer("document-service")
		if err != nil {
			return err
		}
		defer cancel(ctx)

		reflectorNames := []string{}
		reflectorNames = append(reflectorNames, docRegister()...)
		reflectorRegister(reflectorNames)

		port := os.Getenv("PORT")
		if port == "" {
			port = "8087"
		}
		host := os.Getenv("HOST")
		listen := fmt.Sprintf("%s:%s", host, port)
		log.Printf("running document-service on %s\n", listen)

		// Use h2c so we can serve HTTP/2 without TLS.
		return http.ListenAndServe(
			listen,
			h2c.NewHandler(custom_connect.WithCORS(mux), &http2.Server{}),
		)
	}
}
