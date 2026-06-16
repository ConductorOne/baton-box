package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	ClientID = field.StringField(
		"box-client-id",
		field.WithDisplayName("Client ID"),
		field.WithDescription("Client ID used to authenticate to the Box API."),
		field.WithRequired(true),
	)

	ClientSecret = field.StringField(
		"box-client-secret",
		field.WithDisplayName("Client Secret"),
		field.WithDescription("Client Secret used to authenticate to the Box API."),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)

	EnterpriseID = field.StringField(
		"enterprise-id",
		field.WithDisplayName("Enterprise ID"),
		field.WithDescription("ID of your Box enterprise."),
		field.WithRequired(true),
	)
	BaseURLField = field.StringField(
		"base-url",
		field.WithDescription("Override the Box API URL (for testing or enterprise deployments)"),
		field.WithHidden(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)
)

//go:generate go run ./gen
var Config = field.NewConfiguration([]field.SchemaField{
	ClientID,
	ClientSecret,
	EnterpriseID,
	BaseURLField,
},
	field.WithConnectorDisplayName("Box"),
	field.WithIconUrl("/static/app-icons/box.svg"),
	field.WithHelpUrl("/docs/baton/box"),
)
