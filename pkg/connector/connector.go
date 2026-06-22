package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-box/pkg/box"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

var (
	resourceTypeUser = &v2.ResourceType{
		Id:          user,
		DisplayName: "User",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_USER,
		},
		Annotations: annotationsForUserResourceType(),
	}
	resourceTypeGroup = &v2.ResourceType{
		Id:          "group",
		DisplayName: "Group",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_GROUP,
		},
		Annotations: annotations.New(capabilityPermissions("Manage groups")),
	}
	resourceTypeEnterprise = &v2.ResourceType{
		Id:          "enterprise",
		DisplayName: "Enterprise",
		Annotations: annotations.New(capabilityPermissions("Manage enterprise properties")),
	}
	resourceTypeRole = &v2.ResourceType{
		Id:          fieldRole,
		DisplayName: "Role",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_ROLE,
		},
		Annotations: annotations.New(capabilityPermissions("Manage users")),
	}
)

type Box struct {
	client *box.Client
}

func New(ctx context.Context, clientId string, clientSecret string, enterpriseId string, baseURL string) (*Box, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, err
	}

	token, err := box.RequestAccessToken(ctx, clientId, clientSecret, enterpriseId, baseURL)
	if err != nil {
		return nil, fmt.Errorf("baton-box: failed to get token: %w", err)
	}

	client, err := box.NewClient(httpClient, token, baseURL)
	if err != nil {
		return nil, err
	}
	return &Box{client: client}, nil
}

func (b *Box) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Box",
		Description: "Connector syncing users, groups, enterprise and roles from Box to Baton",
		AccountCreationSchema: &v2.ConnectorAccountCreationSchema{
			FieldMap: map[string]*v2.ConnectorAccountCreationSchema_Field{
				fieldLogin: {
					DisplayName: "Email (Login)",
					Required:    true,
					Description: "The email address used as the Box login. Must be unique within the enterprise.",
					Placeholder: "john.doe@example.com",
					Order:       1,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
				},
				fieldName: {
					DisplayName: "Full Name",
					Required:    true,
					Description: "The display name shown in Box.",
					Placeholder: "John Doe",
					Order:       2,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
				},
			},
		},
	}, nil
}

func (b *Box) Validate(ctx context.Context) (annotations.Annotations, error) {
	if _, err := b.client.GetCurrentUserWithEnterprise(ctx); err != nil {
		return nil, fmt.Errorf("baton-box: failed to authenticate: %w", err)
	}

	// Box CCG Service Accounts are not assigned a role designation (role != "admin"),
	// so role-based validation incorrectly rejects valid setups. Validate admin scope
	// by exercising a manage-users endpoint: a 403 signals insufficient scope.
	if err := b.client.CheckAdminAccess(ctx); err != nil {
		return nil, fmt.Errorf("baton-box: token lacks admin scope (manage users): %w", err)
	}

	return nil, nil
}

var _ connectorbuilder.ConnectorBuilderV2 = (*Box)(nil)

func (b *Box) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncerV2 {
	return []connectorbuilder.ResourceSyncerV2{
		userBuilder(b.client),
		groupBuilder(b.client),
		enterpriseBuilder(b.client),
		roleBuilder(b.client),
	}
}
