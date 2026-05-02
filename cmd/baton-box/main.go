package main

import (
	"context"
	"fmt"
	"os"

	"github.com/conductorone/baton-box/pkg/connector"
	cfg "github.com/conductorone/baton-box/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/connectorrunner"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

var version = "dev"

func main() {
	ctx := context.Background()

	_, cmd, err := config.DefineConfiguration(
		ctx,
		"baton-box",
		getConnector,
		cfg.Config,
		connectorrunner.WithDefaultCapabilitiesConnectorBuilder(&connector.Box{}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	cmd.Version = version

	err = cmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func getConnector(ctx context.Context, c *cfg.Box) (types.ConnectorServer, error) {
	l := ctxzap.Extract(ctx)

	cb, err := connector.New(ctx, c.BoxClientId, c.BoxClientSecret, c.EnterpriseId, c.BaseUrl)
	if err != nil {
		l.Error("error creating box connector", zap.Error(err))
		return nil, err
	}

	conn, err := connectorbuilder.NewConnector(ctx, cb)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}

	return conn, nil
}
