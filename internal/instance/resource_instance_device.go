package instance

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	incus "github.com/lxc/incus/v7/client"

	"github.com/lxc/terraform-provider-incus/internal/common"
	"github.com/lxc/terraform-provider-incus/internal/errors"
	provider_config "github.com/lxc/terraform-provider-incus/internal/provider-config"
)

type InstanceDeviceModel struct {
	Instance   types.String `tfsdk:"instance"`
	Name       types.String `tfsdk:"name"`
	Type       types.String `tfsdk:"type"`
	Properties types.Map    `tfsdk:"properties"`
	Project    types.String `tfsdk:"project"`
	Remote     types.String `tfsdk:"remote"`
	Local      types.Bool   `tfsdk:"local"`
}

type InstanceDeviceResource struct {
	provider *provider_config.IncusProviderConfig
}

func NewInstanceDeviceResource() resource.Resource {
	return &InstanceDeviceResource{}
}

func (r InstanceDeviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = fmt.Sprintf("%s_instance_device", req.ProviderTypeName)
}

func (r InstanceDeviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"instance": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf(
						"none", "disk", "nic", "unix-char",
						"unix-block", "usb", "gpu", "infiniband",
						"proxy", "unix-hotplug", "tpm", "pci",
					),
				},
			},
			"properties": schema.MapAttribute{
				Required:    true,
				ElementType: types.StringType,
				Validators: []validator.Map{
					mapvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"project": schema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"remote": schema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"local": schema.BoolAttribute{
				Computed: true,
			},
		},
	}
}

func (r *InstanceDeviceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data := req.ProviderData
	if data == nil {
		return
	}

	provider, ok := data.(*provider_config.IncusProviderConfig)
	if !ok {
		resp.Diagnostics.Append(errors.NewProviderDataTypeError(req.ProviderData))
		return
	}

	r.provider = provider
}

func (r InstanceDeviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan InstanceDeviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := r.server(plan)
	if err != nil {
		resp.Diagnostics.Append(errors.NewInstanceServerError(err))
		return
	}

	instance, etag, err := server.GetInstance(plan.Instance.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to retrieve instance %q", plan.Instance.ValueString()), err.Error())
		return
	}

	deviceName := plan.Name.ValueString()
	if _, exists := instance.Devices[deviceName]; exists {
		resp.Diagnostics.AddError(
			"Instance device already exists locally",
			fmt.Sprintf("Device %q on instance %q is already managed locally. Refusing to adopt or replace it implicitly.", deviceName, plan.Instance.ValueString()),
		)
		return
	}

	effectiveDevice, exists := instance.ExpandedDevices[deviceName]
	if !exists {
		effectiveDevice = make(map[string]string)
		deviceType := plan.Type.ValueString()
		if deviceType == "" {
			resp.Diagnostics.AddError("Missing instance device type", fmt.Sprintf("Device %q does not exist and requires a type.", deviceName))
			return
		}
		effectiveDevice["type"] = deviceType
	}

	deviceProperties, diags := common.ToConfigMap(ctx, plan.Properties)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	localDevice := cloneDevice(effectiveDevice)
	for key, value := range deviceProperties {
		localDevice[key] = value
	}

	newInstance := instance.Writable()
	newInstance.Devices = cloneDevices(instance.Devices)
	newInstance.Devices[deviceName] = localDevice

	op, err := server.UpdateInstance(instance.Name, newInstance, etag)
	if err == nil {
		err = op.WaitContext(ctx)
	}
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to create device %q on instance %q", deviceName, plan.Instance.ValueString()), err.Error())
		return
	}

	resp.Diagnostics.Append(r.syncState(ctx, &resp.State, server, plan)...)
}

func (r InstanceDeviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state InstanceDeviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := r.server(state)
	if err != nil {
		resp.Diagnostics.Append(errors.NewInstanceServerError(err))
		return
	}

	resp.Diagnostics.Append(r.syncState(ctx, &resp.State, server, state)...)
}

func (r InstanceDeviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan InstanceDeviceModel
	var state InstanceDeviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := r.server(plan)
	if err != nil {
		resp.Diagnostics.Append(errors.NewInstanceServerError(err))
		return
	}

	instanceName := plan.Instance.ValueString()
	instance, etag, err := server.GetInstance(instanceName)
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to retrieve instance %q", instanceName), err.Error())
		return
	}

	deviceName := plan.Name.ValueString()
	localDevice, localExists := instance.Devices[deviceName]
	if !localExists {
		var exists bool
		localDevice, exists = instance.ExpandedDevices[deviceName]
		if !exists {
			resp.Diagnostics.AddError("Instance device disappeared", fmt.Sprintf("Device %q no longer exists on instance %q.", deviceName, instanceName))
			return
		}
		localDevice = cloneDevice(localDevice)
	}

	oldProperties := make(map[string]*string)
	resp.Diagnostics.Append(state.Properties.ElementsAs(ctx, &oldProperties, false)...)
	newProperties, diags := common.ToConfigMap(ctx, plan.Properties)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	for key := range oldProperties {
		if _, exists := newProperties[key]; !exists {
			delete(localDevice, key)
		}
	}
	for key, value := range newProperties {
		localDevice[key] = value
	}

	if !localExists {
		instance.Devices = cloneDevices(instance.Devices)
		instance.Devices[deviceName] = localDevice
	}

	newInstance := instance.Writable()
	newInstance.Devices = cloneDevices(instance.Devices)
	op, err := server.UpdateInstance(instanceName, newInstance, etag)
	if err == nil {
		err = op.WaitContext(ctx)
	}
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to update device %q on instance %q", deviceName, instanceName), err.Error())
		return
	}

	resp.Diagnostics.Append(r.syncState(ctx, &resp.State, server, plan)...)
}

func (r InstanceDeviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state InstanceDeviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := r.server(state)
	if err != nil {
		resp.Diagnostics.Append(errors.NewInstanceServerError(err))
		return
	}

	instanceName := state.Instance.ValueString()
	instance, etag, err := server.GetInstance(instanceName)
	if err != nil {
		if errors.IsNotFoundError(err) {
			return
		}
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to retrieve instance %q", instanceName), err.Error())
		return
	}

	deviceName := state.Name.ValueString()
	if _, exists := instance.Devices[deviceName]; !exists {
		return
	}

	newInstance := instance.Writable()
	newInstance.Devices = cloneDevices(instance.Devices)
	delete(newInstance.Devices, deviceName)
	op, err := server.UpdateInstance(instanceName, newInstance, etag)
	if err == nil {
		err = op.WaitContext(ctx)
	}
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to delete device %q from instance %q", deviceName, instanceName), err.Error())
	}
}

func (r *InstanceDeviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	meta := common.ImportMetadata{
		ResourceName:   "instance_device",
		RequiredFields: []string{"instance", "name"},
	}

	fields, diag := meta.ParseImportID(req.ID)
	if diag != nil {
		resp.Diagnostics.Append(diag)
		return
	}

	for key, value := range fields {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(key), value)...)
	}
}

func (r InstanceDeviceResource) server(model InstanceDeviceModel) (incus.InstanceServer, error) {
	return r.provider.InstanceServer(model.Remote.ValueString(), model.Project.ValueString(), "")
}

func (r InstanceDeviceResource) syncState(ctx context.Context, tfState *tfsdk.State, server incus.InstanceServer, model InstanceDeviceModel) diag.Diagnostics {
	instanceName := model.Instance.ValueString()
	instance, _, err := server.GetInstance(instanceName)
	if err != nil {
		if errors.IsNotFoundError(err) {
			tfState.RemoveResource(ctx)
			return nil
		}
		return diag.Diagnostics{diag.NewErrorDiagnostic(fmt.Sprintf("Failed to retrieve instance %q", instanceName), err.Error())}
	}

	deviceName := model.Name.ValueString()
	effectiveDevice, effectiveExists := instance.ExpandedDevices[deviceName]
	if !effectiveExists {
		if localDevice, localExists := instance.Devices[deviceName]; localExists {
			effectiveDevice = localDevice
			effectiveExists = true
		}
	}
	if !effectiveExists {
		tfState.RemoveResource(ctx)
		return nil
	}

	managedProperties := make(map[string]*string)
	if model.Properties.IsNull() || model.Properties.IsUnknown() {
		for key, value := range effectiveDevice {
			if key == "type" {
				continue
			}
			value := value
			managedProperties[key] = &value
		}
	} else {
		diags := model.Properties.ElementsAs(ctx, &managedProperties, false)
		if diags.HasError() {
			return diags
		}
		for key := range managedProperties {
			value, exists := effectiveDevice[key]
			if !exists {
				managedProperties[key] = nil
				continue
			}
			valueCopy := value
			managedProperties[key] = &valueCopy
		}
	}

	properties, diags := types.MapValueFrom(ctx, types.StringType, managedProperties)
	if diags.HasError() {
		return diags
	}

	model.Type = types.StringValue(effectiveDevice["type"])
	model.Properties = properties
	_, local := instance.Devices[deviceName]
	model.Local = types.BoolValue(local)
	return tfState.Set(ctx, &model)
}

func cloneDevices(devices map[string]map[string]string) map[string]map[string]string {
	result := make(map[string]map[string]string, len(devices))
	for name, device := range devices {
		result[name] = cloneDevice(device)
	}
	return result
}

func cloneDevice(device map[string]string) map[string]string {
	result := make(map[string]string, len(device))
	for key, value := range device {
		result[key] = value
	}
	return result
}
