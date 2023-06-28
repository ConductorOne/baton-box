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

type enterpriseResourceType struct {
	resourceType *v2.ResourceType
	client       *box.Client
}

func (o *enterpriseResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

func enterpriseBuilder(client *box.Client) *enterpriseResourceType {
	return &enterpriseResourceType{
		resourceType: resourceTypeEnterprise,
		client:       client,
	}
}

// Create a new connector resource for a Box enterprise.
func enterpriseResource(ctx context.Context, policy box.Enterprise) (*v2.Resource, error) {
	policyOptions := []rs.ResourceOption{
		rs.WithAnnotation(
			&v2.ChildResourceType{ResourceTypeId: resourceTypeUser.Id},
			&v2.ChildResourceType{ResourceTypeId: resourceTypeGroup.Id},
			&v2.ChildResourceType{ResourceTypeId: resourceTypeRole.Id},
		),
	}

	ret, err := rs.NewResource(policy.Name, resourceTypeEnterprise, policy.ID, policyOptions...)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (o *enterpriseResourceType) List(ctx context.Context, resourceId *v2.ResourceId, pt *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var rv []*v2.Resource
	// there is no endpoint just for enterprise so we have to get the current user with enterprise data.
	currentUser, err := o.client.GetCurrentUserWithEnterprise(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	pr, err := enterpriseResource(ctx, currentUser.Enterprise)
	if err != nil {
		return nil, "", nil, err
	}

	rv = append(rv, pr)
	return rv, "", nil, nil
}

func (o *enterpriseResourceType) Entitlements(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	assigmentOptions := []ent.EntitlementOption{
		ent.WithGrantableTo(resourceTypeUser),
		ent.WithDescription(fmt.Sprintf("%s of %s Box enterprise", member, resource.DisplayName)),
		ent.WithDisplayName(fmt.Sprintf("%s enterprise %s", resource.DisplayName, member)),
	}

	assignmentEn := ent.NewAssignmentEntitlement(resource, member, assigmentOptions...)
	rv = append(rv, assignmentEn)

	return rv, "", nil, nil
}

func (o *enterpriseResourceType) Grants(ctx context.Context, resource *v2.Resource, pt *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	users, err := o.client.GetUsers(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("box-connector: failed to list users: %w", err)
	}

	var rv []*v2.Grant
	for _, user := range users {
		userCopy := user
		ur, err := userResource(&userCopy, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}
		membershipGrant := grant.NewGrant(resource, member, ur.Id)
		rv = append(rv, membershipGrant)
	}

	return rv, "", nil, nil
}
