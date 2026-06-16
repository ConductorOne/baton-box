package connector

import (
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func annotationsForUserResourceType() annotations.Annotations {
	annos := annotations.Annotations{}
	annos.Update(&v2.SkipEntitlementsAndGrants{})
	annos.Update(capabilityPermissions("Manage users"))
	return annos
}

func capabilityPermissions(perms ...string) *v2.CapabilityPermissions {
	var permissions []*v2.CapabilityPermission
	for _, p := range perms {
		permissions = append(permissions, v2.CapabilityPermission_builder{Permission: p}.Build())
	}
	return v2.CapabilityPermissions_builder{Permissions: permissions}.Build()
}

func titleCase(s string) string {
	titleCaser := cases.Title(language.English)

	return titleCaser.String(s)
}

// Populate entitlement options for a 1password resource.
func PopulateOptions(displayName, permission, resource string) []ent.EntitlementOption {
	options := []ent.EntitlementOption{
		ent.WithGrantableTo(resourceTypeUser),
		ent.WithDescription(fmt.Sprintf("%s of Box %s %s", permission, displayName, resource)),
		ent.WithDisplayName(fmt.Sprintf("%s %s %s", displayName, resource, permission)),
	}
	return options
}
