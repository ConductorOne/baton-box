package connector

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-box/pkg/box"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	resource "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type roleResourceType struct {
	resourceType *v2.ResourceType
	client       *box.Client
}

func (o *roleResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

var roles = map[string]string{
	admin:     admin,
	"coadmin": "co-admin",
	user:      user,
}

// roleOrder defines a stable iteration order for the roles map.
var roleOrder = []string{admin, "co-admin", user}

// Create a new connector resource for a Box role.
func roleResource(ctx context.Context, role string, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	roleDisplayName := titleCase(role)
	profile := map[string]interface{}{
		"role_name": roleDisplayName,
		"role_id":   role,
	}

	roleTraitOptions := []resource.RoleTraitOption{
		resource.WithRoleProfile(profile),
	}

	ret, err := resource.NewRoleResource(
		roleDisplayName,
		resourceTypeRole,
		role,
		roleTraitOptions,
		resource.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (o *roleResourceType) List(ctx context.Context, parentId *v2.ResourceId, _ resource.SyncOpAttrs) ([]*v2.Resource, *resource.SyncOpResults, error) {
	if parentId == nil {
		return nil, &resource.SyncOpResults{}, nil
	}

	var rv []*v2.Resource
	for _, role := range roleOrder {
		rr, err := roleResource(ctx, role, parentId)
		if err != nil {
			return nil, nil, err
		}
		rv = append(rv, rr)
	}

	return rv, &resource.SyncOpResults{}, nil
}

func (o *roleResourceType) Entitlements(_ context.Context, res *v2.Resource, _ resource.SyncOpAttrs) ([]*v2.Entitlement, *resource.SyncOpResults, error) {
	var rv []*v2.Entitlement

	permissionOptions := []ent.EntitlementOption{
		ent.WithGrantableTo(resourceTypeUser),
		ent.WithDescription(fmt.Sprintf("%s Box role", res.DisplayName)),
		ent.WithDisplayName(fmt.Sprintf("%s role %s", res.DisplayName, member)),
	}

	permissionEn := ent.NewPermissionEntitlement(res, member, permissionOptions...)
	rv = append(rv, permissionEn)
	return rv, &resource.SyncOpResults{}, nil
}

func (o *roleResourceType) Grants(ctx context.Context, res *v2.Resource, _ resource.SyncOpAttrs) ([]*v2.Grant, *resource.SyncOpResults, error) {
	users, err := o.client.GetUsers(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("box-connector: failed to list users: %w", err)
	}

	var rv []*v2.Grant
	for _, user := range users {
		userCopy := user
		ur, err := userResource(&userCopy, res.Id)
		if err != nil {
			return nil, nil, err
		}

		if res.Id.Resource == roles[user.Role] {
			permissionGrant := grant.NewGrant(res, member, ur.Id)
			rv = append(rv, permissionGrant)
		}
	}

	return rv, &resource.SyncOpResults{}, nil
}

func roleBuilder(client *box.Client) *roleResourceType {
	return &roleResourceType{
		resourceType: resourceTypeRole,
		client:       client,
	}
}

// Compile-time interface assertion.
var _ connectorbuilder.ResourceProvisionerV2 = (*roleResourceType)(nil)

// boxRoleKey maps a role resource ID (e.g. "co-admin") to the Box API role value (e.g. "coadmin").
func boxRoleKey(resourceID string) string {
	for apiKey, displayName := range roles {
		if displayName == resourceID {
			return apiKey
		}
	}
	return resourceID
}

// Grant sets a Box user's enterprise role to the granted role.
// The Box API does not allow assigning the "admin" role via PUT /users/{id};
// that role can only be set through the Box Admin Console.
func (o *roleResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	userID := principal.GetId().GetResource()
	roleID := entitlement.GetResource().GetId().GetResource()

	apiRole := boxRoleKey(roleID)
	if apiRole == admin {
		return nil, nil, status.Error(codes.InvalidArgument, "baton-box: the admin role cannot be assigned via the Box API; use the Box Admin Console instead")
	}

	if _, err := o.client.UpdateUser(ctx, userID, map[string]interface{}{fieldRole: apiRole}); err != nil {
		return nil, nil, fmt.Errorf("baton-box: failed to grant role %s to user %s: %w", roleID, userID, err)
	}
	return []*v2.Grant{grant.NewGrant(entitlement.GetResource(), member, principal.GetId())}, nil, nil
}

// Revoke downgrades a Box user's enterprise role to the base "user" role.
// Revoking the "user" role itself is a no-op. The admin role cannot be
// modified via the Box API — Box rejects it with 403 regardless of caller.
func (o *roleResourceType) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	userID := g.GetPrincipal().GetId().GetResource()
	roleID := g.GetEntitlement().GetResource().GetId().GetResource()

	if roleID == admin {
		return nil, status.Error(codes.InvalidArgument, "baton-box: the admin role cannot be modified via the Box API; use the Box Admin Console instead")
	}

	if roleID == user {
		return annotations.New(&v2.GrantAlreadyRevoked{}), nil
	}

	if _, err := o.client.UpdateUser(ctx, userID, map[string]interface{}{fieldRole: user}); err != nil {
		return nil, fmt.Errorf("baton-box: failed to revoke role %s from user %s: %w", roleID, userID, err)
	}
	return nil, nil
}
