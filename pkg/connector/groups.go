package connector

import (
	"context"
	"fmt"

	"github.com/ConductorOne/baton-box/pkg/box"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

const (
	memberEntitlement = "member"
	adminEntitlement  = "admin"
)

var accessLevels = map[string]string{
	"admins_only":        "admins only",
	"admins_and_members": "admins and members",
	"all_managed_users":  "all managed users",
}

var entitlements = []string{
	memberEntitlement,
	adminEntitlement,
}

type groupResourceType struct {
	resourceType *v2.ResourceType
	client       *box.Client
}

func (g *groupResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return g.resourceType
}

// Create a new connector resource for a Box group.
func groupResource(group *box.Group, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"group_id":   group.ID,
		"group_name": group.Name,
	}

	groupTraitOptions := []rs.GroupTraitOption{rs.WithGroupProfile(profile)}

	ret, err := rs.NewGroupResource(
		group.Name,
		resourceTypeGroup,
		group.ID,
		groupTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (g *groupResourceType) List(ctx context.Context, parentId *v2.ResourceId, token *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	groups, err := g.client.GetGroups(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("box-connector: failed to list groups: %w", err)
	}

	var rv []*v2.Resource
	for _, group := range groups {
		groupCopy := group
		ur, err := groupResource(&groupCopy, parentId)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, ur)
	}

	return rv, "", nil, nil
}

func (g *groupResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	for _, entitlement := range entitlements {
		assigmentOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeUser),
			ent.WithDescription(fmt.Sprintf("%s of %s Group in Box", entitlement, resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s Group %s", resource.DisplayName, entitlement)),
		}

		en := ent.NewAssignmentEntitlement(resource, entitlement, assigmentOptions...)
		rv = append(rv, en)
	}

	for _, level := range accessLevels {
		invitabilityOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeGroup),
			ent.WithDescription(fmt.Sprintf("Invitability level for %s group", resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s group %s invite", resource.DisplayName, level)),
		}

		viewabilityOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeGroup),
			ent.WithDescription(fmt.Sprintf("Member viewability level for %s group", resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s group %s view", resource.DisplayName, level)),
		}

		viewabilityEn := ent.NewPermissionEntitlement(resource, fmt.Sprintf("%s view", level), viewabilityOptions...)
		invitabilityEn := ent.NewPermissionEntitlement(resource, fmt.Sprintf("%s invite", level), invitabilityOptions...)
		rv = append(rv, viewabilityEn, invitabilityEn)
	}

	return rv, "", nil, nil
}

func (g *groupResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant

	groupMemberships, err := g.client.GetGroupMemberships(ctx, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	groups, err := g.client.GetGroups(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	for _, groupMembership := range groupMemberships {
		ur, err := userResource(&groupMembership.User, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		grant := grant.NewGrant(resource, groupMembership.Role, ur.Id)
		rv = append(rv, grant)
	}

	for _, group := range groups {
		invitabilityLevel, ok := accessLevels[group.InvitabilityLevel]
		if !ok {
			ctxzap.Extract(ctx).Warn("Unknown Box Invitability Level, skipping",
				zap.String("invitability level", group.InvitabilityLevel),
				zap.String("group", group.Name),
			)
			continue
		}

		memberViewabilityLevel, ok := accessLevels[group.MemberViewabilityLevel]
		if !ok {
			ctxzap.Extract(ctx).Warn("Unknown Box Member Viewability Level, skipping",
				zap.String("member viewability level", group.InvitabilityLevel),
				zap.String("group", group.Name),
			)
			continue
		}

		groupCopy := group
		gr, err := groupResource(&groupCopy, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		invitabilityGrant := grant.NewGrant(resource, fmt.Sprintf("%s invite", invitabilityLevel), gr.Id)
		memberViewabilityGrant := grant.NewGrant(resource, fmt.Sprintf("%s view", memberViewabilityLevel), gr.Id)
		rv = append(rv, invitabilityGrant, memberViewabilityGrant)
	}

	return rv, "", nil, nil
}

func groupBuilder(client *box.Client) *groupResourceType {
	return &groupResourceType{
		resourceType: resourceTypeGroup,
		client:       client,
	}
}
