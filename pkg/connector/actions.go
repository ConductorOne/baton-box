package connector

import (
	"context"
	"fmt"

	config "github.com/conductorone/baton-sdk/pb/c1/config/v1"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/actions"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	actionUpdateProfile = "update_profile"
	actionDisableUser   = "disable_user"
	actionEnableUser    = "enable_user"

	argUserResourceIDDisplay = "User Resource ID"
	retSuccessKey            = "success"
	retSuccessDisplay        = "Success"
)

// ACTION_TYPE_ACCOUNT is intentionally omitted from all three schemas: the SDK's
// ResourceActionProvider duplicate-type check rejects the same ActionType appearing
// in more than one action registered for the same resource type.

var successReturnType = []*config.Field{
	{Name: retSuccessKey, DisplayName: retSuccessDisplay, Field: &config.Field_BoolField{}},
}

var updateProfileActionSchema = &v2.BatonActionSchema{
	Name:        actionUpdateProfile,
	DisplayName: "Update User Profile",
	Description: "Updates a Box user's name and/or login (email address)",
	Arguments: []*config.Field{
		{
			Name:        fieldUserID,
			DisplayName: argUserResourceIDDisplay,
			Description: "The Box user ID to update",
			Field:       &config.Field_StringField{},
			IsRequired:  true,
		},
		{
			Name:        fieldName,
			DisplayName: "Full Name",
			Description: "New display name for the user",
			Field:       &config.Field_StringField{},
		},
		{
			Name:        fieldLogin,
			DisplayName: "Email (Login)",
			Description: "New login email address for the user",
			Field:       &config.Field_StringField{},
		},
	},
	ReturnTypes: successReturnType,
	ActionType: []v2.ActionType{
		v2.ActionType_ACTION_TYPE_ACCOUNT_UPDATE_PROFILE,
	},
}

var disableUserActionSchema = &v2.BatonActionSchema{
	Name:        actionDisableUser,
	DisplayName: "Disable User",
	Description: "Sets a Box user's status to inactive (reversible)",
	Arguments: []*config.Field{
		{
			Name:        fieldUserID,
			DisplayName: argUserResourceIDDisplay,
			Description: "The Box user ID to disable",
			Field:       &config.Field_StringField{},
			IsRequired:  true,
		},
	},
	ReturnTypes: successReturnType,
	ActionType: []v2.ActionType{
		v2.ActionType_ACTION_TYPE_ACCOUNT_DISABLE,
	},
}

var enableUserActionSchema = &v2.BatonActionSchema{
	Name:        actionEnableUser,
	DisplayName: "Enable User",
	Description: "Sets a Box user's status back to active",
	Arguments: []*config.Field{
		{
			Name:        fieldUserID,
			DisplayName: argUserResourceIDDisplay,
			Description: "The Box user ID to enable",
			Field:       &config.Field_StringField{},
			IsRequired:  true,
		},
	},
	ReturnTypes: successReturnType,
	ActionType: []v2.ActionType{
		v2.ActionType_ACTION_TYPE_ACCOUNT_ENABLE,
	},
}

var _ connectorbuilder.ResourceActionProvider = (*userResourceType)(nil)

func (o *userResourceType) ResourceActions(ctx context.Context, registry actions.ActionRegistry) error {
	if err := registry.Register(ctx, updateProfileActionSchema, o.updateProfileActionHandler); err != nil {
		return err
	}
	if err := registry.Register(ctx, disableUserActionSchema, o.disableUserActionHandler); err != nil {
		return err
	}
	return registry.Register(ctx, enableUserActionSchema, o.enableUserActionHandler)
}

func (o *userResourceType) updateProfileActionHandler(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
	if args == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "invalid arguments")
	}

	userID, err := extractStringArg(args, fieldUserID, true)
	if err != nil {
		return nil, nil, err
	}

	updates := make(map[string]interface{})

	if name, _ := extractStringArg(args, fieldName, false); name != "" {
		updates[fieldName] = name
	}
	if login, _ := extractStringArg(args, fieldLogin, false); login != "" {
		updates[fieldLogin] = login
	}

	if len(updates) == 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "at least one of name or login must be provided")
	}

	if _, err := o.client.UpdateUser(ctx, userID, updates); err != nil {
		return nil, nil, fmt.Errorf("baton-box: failed to update user profile for %s: %w", userID, err)
	}

	return successStruct(), nil, nil
}

func (o *userResourceType) disableUserActionHandler(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
	if args == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "invalid arguments")
	}

	userID, err := extractStringArg(args, fieldUserID, true)
	if err != nil {
		return nil, nil, err
	}

	if err := o.client.DeactivateUser(ctx, userID); err != nil {
		return nil, nil, fmt.Errorf("baton-box: failed to disable user %s: %w", userID, err)
	}

	return successStruct(), nil, nil
}

func (o *userResourceType) enableUserActionHandler(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
	if args == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "invalid arguments")
	}

	userID, err := extractStringArg(args, fieldUserID, true)
	if err != nil {
		return nil, nil, err
	}

	u, err := o.client.GetUser(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-box: failed to get user %s before enabling: %w", userID, err)
	}
	if u.Status == "pending" {
		return nil, nil, status.Errorf(codes.FailedPrecondition, "baton-box: user %s is pending email confirmation and cannot be enabled", userID)
	}

	if err := o.client.ActivateUser(ctx, userID); err != nil {
		return nil, nil, fmt.Errorf("baton-box: failed to enable user %s: %w", userID, err)
	}

	return successStruct(), nil, nil
}

// extractStringArg reads a string field from a structpb.Struct.
// If required is true and the field is absent or empty, it returns an InvalidArgument error.
func extractStringArg(args *structpb.Struct, key string, required bool) (string, error) {
	val, ok := args.Fields[key]
	if !ok || val == nil {
		if required {
			return "", status.Errorf(codes.InvalidArgument, "missing required argument: %s", key)
		}
		return "", nil
	}
	sv, ok := val.GetKind().(*structpb.Value_StringValue)
	if !ok {
		if required {
			return "", status.Errorf(codes.InvalidArgument, "%s must be a string", key)
		}
		return "", nil
	}
	if required && sv.StringValue == "" {
		return "", status.Errorf(codes.InvalidArgument, "%s must not be empty", key)
	}
	return sv.StringValue, nil
}

func successStruct() *structpb.Struct {
	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			retSuccessKey: {Kind: &structpb.Value_BoolValue{BoolValue: true}},
		},
	}
}
