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
)

const (
	member           = "member"
	admin            = "admin"
	allManagedUsers  = "all_managed_users"
	adminsOnly       = "admins_only"
	adminsAndMembers = "admins_and_members"
)

var accessLevels = map[string]string{
	adminsOnly:       "admins only",
	adminsAndMembers: "admins and members",
	allManagedUsers:  "all managed users",
}

var entitlements = []string{
	member,
	admin,
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
			ent.WithDescription(fmt.Sprintf("%s of %s group in Box", titleCaser.String(entitlement), resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s group %s", resource.DisplayName, entitlement)),
		}

		en := ent.NewAssignmentEntitlement(resource, entitlement, assigmentOptions...)
		rv = append(rv, en)
	}

	for _, level := range accessLevels {
		permissionOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeUser),
			ent.WithDescription(fmt.Sprintf("View and invite permission for %s group", resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s permissions for %s group", titleCaser.String(level), resource.DisplayName)),
		}

		permissionEn := ent.NewPermissionEntitlement(resource, level, permissionOptions...)
		rv = append(rv, permissionEn)
	}

	return rv, "", nil, nil
}

func (g *groupResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant

	group, err := g.client.GetGroup(ctx, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	if group.MemberViewabilityLevel == allManagedUsers {
		allUsers, err := g.client.GetUsers(ctx)
		if err != nil {
			return nil, "", nil, err
		}

		for _, user := range allUsers {
			userCopy := user
			ur, err := userResource(&userCopy, resource.Id)
			if err != nil {
				return nil, "", nil, err
			}

			grant := grant.NewGrant(resource, accessLevels[allManagedUsers], ur.Id)

			rv = append(rv, grant)
		}
	}

	groupMemberships, err := g.client.GetGroupMemberships(ctx, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	for _, groupMembership := range groupMemberships {
		ur, err := userResource(&groupMembership.User, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}
		membershipGrant := grant.NewGrant(resource, groupMembership.Role, ur.Id)
		rv = append(rv, membershipGrant)

		if group.MemberViewabilityLevel == adminsOnly && groupMembership.Role == admin {
			adminsGrant := grant.NewGrant(resource, accessLevels[adminsOnly], ur.Id)
			rv = append(rv, adminsGrant)
		} else if group.MemberViewabilityLevel == adminsAndMembers {
			membersGrant := grant.NewGrant(resource, accessLevels[adminsAndMembers], ur.Id)
			rv = append(rv, membersGrant)
		}
	}

	return rv, "", nil, nil
}

func groupBuilder(client *box.Client) *groupResourceType {
	return &groupResourceType{
		resourceType: resourceTypeGroup,
		client:       client,
	}
}
