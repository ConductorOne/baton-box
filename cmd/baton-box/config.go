package main

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/spf13/cobra"
)

// config defines the external configuration required for the connector to run.
type config struct {
	cli.BaseConfig `mapstructure:",squash"` // Puts the base config options in the same place as the connector options

	ClientID     string `mapstructure:"box-client-id"`
	ClientSecret string `mapstructure:"box-client-secret"`
	EnterpriseID string `mapstructure:"enterprise-id"`
}

// validateConfig is run after the configuration is loaded, and should return an error if it isn't valid.
func validateConfig(ctx context.Context, cfg *config) error {
	if cfg.ClientID == "" {
		return fmt.Errorf("box client id is missing")
	}
	if cfg.ClientSecret == "" {
		return fmt.Errorf("box client secret is missing")
	}
	if cfg.EnterpriseID == "" {
		return fmt.Errorf("enterprise id is missing")
	}
	return nil
}

// cmdFlags sets the cmdFlags required for the connector.
func cmdFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("box-client-id", "", "Client ID used to authenticate to the Box API. ($BATON_BOX_CLIENT_ID)")
	cmd.PersistentFlags().String("box-client-secret", "", "Client Secret used to authenticate to the Box API. ($BATON_BOX_CLIENT_SECRET)")
	cmd.PersistentFlags().String("enterprise-id", "", "ID of your Box enterprise. ($BATON_ENTERPRISE_ID)")
}
