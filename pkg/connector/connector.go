package connector

import (
	"context"
	"fmt"

	"github.com/ConductorOne/baton-box/pkg/box"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

var (
	resourceTypeUser = &v2.ResourceType{
		Id:          "user",
		DisplayName: "User",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_USER,
		},
	}
	resourceTypeGroup = &v2.ResourceType{
		Id:          "group",
		DisplayName: "Group",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_GROUP,
		},
	}
	resourceTypeEnterprise = &v2.ResourceType{
		Id:          "enterprise",
		DisplayName: "Enterprise",
	}
	resourceTypeRole = &v2.ResourceType{
		Id:          "role",
		DisplayName: "Role",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_ROLE,
		},
	}
)

type Box struct {
	client *box.Client
}

func New(ctx context.Context, clientId string, clientSecret string, enterpriseId string) (*Box, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, err
	}

	token, err := box.RequestAccessToken(ctx, clientId, clientSecret, enterpriseId)
	if err != nil {
		return nil, fmt.Errorf("box-connector: failed to get token: %w", err)
	}

	return &Box{
		client: box.NewClient(httpClient, token),
	}, nil
}

func (b *Box) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Box",
	}, nil
}

func (b *Box) Validate(ctx context.Context) (annotations.Annotations, error) {
	currentUser, err := b.client.GetCurrentUserWithEnterprise(ctx)
	if err != nil {
		return nil, fmt.Errorf("box-connector: failed to authenticate: %w", err)
	}

	if currentUser.Role != "admin" {
		return nil, fmt.Errorf("box-connector: user is not an admin")
	}

	return nil, nil
}

func (b *Box) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncer {
	return []connectorbuilder.ResourceSyncer{
		userBuilder(b.client),
		groupBuilder(b.client),
		enterpriseBuilder(b.client),
		roleBuilder(b.client),
	}
}
