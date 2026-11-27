package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/conductorone/baton-box/pkg/box"
	config "github.com/conductorone/baton-sdk/pb/c1/config/v1"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/actions"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	actionUpdateProfile = "update_profile"
	actionDisableUser   = "disable_user"
	actionEnableUser    = "enable_user"
	actionTransferFiles = "transfer_user_files"

	argUserResourceIDDisplay = "User Resource ID"
	argTargetUserID          = "target_user_id"
	argNotify                = "notify"
	retSuccessKey            = "success"
	retSuccessDisplay        = "Success"
	retFolderIDKey           = "folder_id"
	retFolderNameKey         = "folder_name"
	retStatusKey             = "status"

	transferStatusCompleted  = "completed"
	transferStatusInProgress = "in_progress"
)

// transferWaitTimeout is how long the transfer action waits for Box to answer.
// Box holds the request open while it moves content (up to 10 minutes), which is
// longer than a connector action may run on the platform, so after this window the
// action stops waiting and reports the transfer as in progress. 150s assumes the
// default Lambda deployment, where C1 waits for an action for the Lambda timeout
// (300s by default) minus one minute. With a shorter Lambda timeout C1 can mark the
// action failed while it still waits; without Lambda, C1 waits longer and a transfer
// that would have finished can be reported as in progress. A variable so tests can
// shorten it.
var transferWaitTimeout = 150 * time.Second

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

var transferUserFilesActionSchema = &v2.BatonActionSchema{
	Name:        actionTransferFiles,
	DisplayName: "Transfer User Files",
	Description: "Moves all files and folders a Box user owns into a new folder in another user's account. " +
		"Returns status completed when Box finishes within 2.5 minutes, or in_progress when the transfer is still running in Box. " +
		"Do not deprovision the source user while the status is in_progress: unless Keep users who still own content is on, that deletes the content that has not moved yet.",
	Arguments: []*config.Field{
		{
			Name:        fieldUserID,
			DisplayName: "Source User Resource ID",
			Description: "The Box user ID to transfer files and folders from",
			Field:       &config.Field_StringField{},
			IsRequired:  true,
		},
		{
			Name:        argTargetUserID,
			DisplayName: "Target User Resource ID",
			Description: "The Box user ID to receive the files and folders. Must be an active user.",
			Field:       &config.Field_StringField{},
			IsRequired:  true,
		},
		{
			Name:        argNotify,
			DisplayName: "Notify",
			Description: "Whether Box sends email notifications about the transfer. Defaults to false.",
			Field:       &config.Field_BoolField{},
		},
	},
	ReturnTypes: []*config.Field{
		{Name: retSuccessKey, DisplayName: retSuccessDisplay, Field: &config.Field_BoolField{}},
		{
			Name:        retStatusKey,
			DisplayName: "Transfer Status",
			Description: "completed when Box finished the transfer, or in_progress when Box is still moving content. " +
				"Wait before deprovisioning the source user while it is in_progress.",
			Field: &config.Field_StringField{},
		},
		{
			Name:        retFolderIDKey,
			DisplayName: "Folder ID",
			Description: "ID of the folder Box created in the target user's account. Empty while the transfer is in progress.",
			Field:       &config.Field_StringField{},
		},
		{
			Name:        retFolderNameKey,
			DisplayName: "Folder Name",
			Description: "Name of the folder in the target user's account that holds the transferred content",
			Field:       &config.Field_StringField{},
		},
	},
	ActionType: []v2.ActionType{
		v2.ActionType_ACTION_TYPE_DYNAMIC,
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
	if err := registry.Register(ctx, enableUserActionSchema, o.enableUserActionHandler); err != nil {
		return err
	}
	return registry.Register(ctx, transferUserFilesActionSchema, o.transferUserFilesActionHandler)
}

func (o *userResourceType) updateProfileActionHandler(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
	if args == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "baton-box: invalid arguments")
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
		return nil, nil, status.Error(codes.InvalidArgument, "baton-box: at least one of name or login must be provided")
	}

	if _, err := o.client.UpdateUser(ctx, userID, updates); err != nil {
		return nil, nil, fmt.Errorf("baton-box: failed to update user profile for %s: %w", userID, err)
	}

	return successStruct(), nil, nil
}

func (o *userResourceType) disableUserActionHandler(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
	if args == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "baton-box: invalid arguments")
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
		return nil, nil, status.Error(codes.InvalidArgument, "baton-box: invalid arguments")
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

func (o *userResourceType) transferUserFilesActionHandler(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
	if args == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "baton-box: invalid arguments")
	}

	sourceID, err := extractStringArg(args, fieldUserID, true)
	if err != nil {
		return nil, nil, err
	}
	targetID, err := extractStringArg(args, argTargetUserID, true)
	if err != nil {
		return nil, nil, err
	}
	sourceID, targetID = strings.TrimSpace(sourceID), strings.TrimSpace(targetID)
	if sourceID == "" || targetID == "" {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-box: %s and %s must not be empty", fieldUserID, argTargetUserID)
	}
	if sourceID == targetID {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-box: %s and %s must be different users", fieldUserID, argTargetUserID)
	}
	notify, err := extractBoolArg(args, argNotify)
	if err != nil {
		return nil, nil, err
	}

	source, err := o.getUserArg(ctx, sourceID, "source")
	if err != nil {
		return nil, nil, err
	}
	target, err := o.getUserArg(ctx, targetID, "target")
	if err != nil {
		return nil, nil, err
	}
	if target.Status != "active" {
		return nil, nil, status.Errorf(codes.FailedPrecondition, "baton-box: target user %s is %s, not active", targetID, target.Status)
	}

	transferCtx, cancel := context.WithTimeout(ctx, transferWaitTimeout)
	defer cancel()

	folder, err := o.client.TransferOwnedItems(transferCtx, sourceID, targetID, notify)
	if err != nil {
		// Box has the request and has not answered: our wait window expired, the
		// response headers timed out, or Box's gateway gave up. A cancelled or expired
		// parent context is a real failure.
		if ctx.Err() == nil && errors.Is(err, box.ErrTransferNoAnswer) {
			ctxzap.Extract(ctx).Debug("baton-box: Box received the transfer request but did not answer in time; reporting it as in progress",
				zap.String("source_user_id", sourceID),
				zap.String("target_user_id", targetID),
				zap.Duration("wait_window", transferWaitTimeout),
				zap.Error(err),
			)
			return transferResult(transferStatusInProgress, "", expectedTransferFolderName(source.Name)), nil, nil
		}
		return nil, nil, fmt.Errorf("baton-box: failed to transfer files from user %s to user %s: %w", sourceID, targetID, err)
	}

	folderName := folder.Name
	if folderName == "" {
		folderName = expectedTransferFolderName(source.Name)
	}
	return transferResult(transferStatusCompleted, folder.ID, folderName), nil, nil
}

// getUserArg fetches the user an action argument names. role ("source" or "target")
// labels the error; a user that does not exist is an InvalidArgument error.
func (o *userResourceType) getUserArg(ctx context.Context, userID, role string) (*box.User, error) {
	u, err := o.client.GetUser(ctx, userID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, status.Errorf(codes.InvalidArgument, "baton-box: %s user %s not found", role, userID)
		}
		return nil, fmt.Errorf("baton-box: failed to get %s user %s: %w", role, userID, err)
	}
	return u, nil
}

// expectedTransferFolderName returns the name Box gives the folder it creates in the
// target user's account: "{User}'s Files and Folders", where {User} is the source
// user's name.
func expectedTransferFolderName(sourceUserName string) string {
	return sourceUserName + "'s Files and Folders"
}

func transferResult(transferStatus, folderID, folderName string) *structpb.Struct {
	ret := successStruct()
	ret.Fields[retStatusKey] = structpb.NewStringValue(transferStatus)
	ret.Fields[retFolderIDKey] = structpb.NewStringValue(folderID)
	ret.Fields[retFolderNameKey] = structpb.NewStringValue(folderName)
	return ret
}

// extractBoolArg reads an optional bool field from a structpb.Struct. An absent field
// reads as false; a field of any other type is an InvalidArgument error.
func extractBoolArg(args *structpb.Struct, key string) (bool, error) {
	val, ok := args.Fields[key]
	if !ok || val == nil {
		return false, nil
	}
	if _, isNull := val.GetKind().(*structpb.Value_NullValue); isNull {
		return false, nil
	}
	bv, ok := val.GetKind().(*structpb.Value_BoolValue)
	if !ok {
		return false, status.Errorf(codes.InvalidArgument, "baton-box: %s must be a boolean", key)
	}
	return bv.BoolValue, nil
}

// extractStringArg reads a string field from a structpb.Struct.
// If required is true and the field is absent or empty, it returns an InvalidArgument error.
func extractStringArg(args *structpb.Struct, key string, required bool) (string, error) {
	val, ok := args.Fields[key]
	if !ok || val == nil {
		if required {
			return "", status.Errorf(codes.InvalidArgument, "baton-box: missing required argument: %s", key)
		}
		return "", nil
	}
	sv, ok := val.GetKind().(*structpb.Value_StringValue)
	if !ok {
		if required {
			return "", status.Errorf(codes.InvalidArgument, "baton-box: %s must be a string", key)
		}
		return "", nil
	}
	if required && sv.StringValue == "" {
		return "", status.Errorf(codes.InvalidArgument, "baton-box: %s must not be empty", key)
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
