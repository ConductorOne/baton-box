package main

import (
	"context"

	"github.com/conductorone/baton-box/pkg/connector"
	cfg "github.com/conductorone/baton-box/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/connectorrunner"
)

var version = "dev"

func main() {
	ctx := context.Background()
	config.RunConnector(
		ctx,
		"baton-box",
		version,
		cfg.Config,
		getConnector,
		connectorrunner.WithDefaultCapabilitiesConnectorBuilderV2(&connector.Box{}),
	)
}

func getConnector(ctx context.Context, c *cfg.Box, _ *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	cb, err := connector.New(ctx, c.BoxClientId, c.BoxClientSecret, c.EnterpriseId, c.BaseUrl)
	if err != nil {
		return nil, nil, err
	}
	return cb, nil, nil
}
