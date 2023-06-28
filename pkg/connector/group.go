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
	member = "member"
	admin  = "admin"
)

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

	assigmentOptions := PopulateOptions(resource.DisplayName, member, resource.Id.Resource)
	assignmentEn := ent.NewAssignmentEntitlement(resource, member, assigmentOptions...)

	permissionOptions := PopulateOptions(resource.DisplayName, admin, resource.Id.Resource)
	permissionEn := ent.NewPermissionEntitlement(resource, admin, permissionOptions...)

	rv = append(rv, assignmentEn, permissionEn)

	return rv, "", nil, nil
}

func (g *groupResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant

	groupMemberships, err := g.client.GetGroupMemberships(ctx, resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	for _, groupMembership := range groupMemberships {
		ur, err := userResource(&groupMembership.User, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}
		membershipGrant := grant.NewGrant(resource, member, ur.Id)
		rv = append(rv, membershipGrant)

		if groupMembership.Role == admin {
			adminsGrant := grant.NewGrant(resource, admin, ur.Id)
			rv = append(rv, adminsGrant)
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
