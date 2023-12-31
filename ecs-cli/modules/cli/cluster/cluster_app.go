// Copyright 2015-2017 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//	http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package cluster

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/cli/cluster/userdata"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/cli/compose/container"
	ecscontext "github.com/aws/amazon-ecs-cli/ecs-cli/modules/cli/compose/context"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/cli/compose/entity/task"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/amimetadata"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/cloudformation"
	ec2client "github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/ec2"
	ecsclient "github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/ecs"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/commands/flags"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/config"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/utils"
	"github.com/aws/aws-sdk-go/aws"
	sdkCFN "github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/docker/libcompose/project"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

// user data builder can be easily mocked in tests
var newUserDataBuilder func(string, []*ecs.Tag) userdata.UserDataBuilder = userdata.NewBuilder

// displayTitle flag is used to print the title for the fields
const displayTitle = true

// Values returned by the ECS Settings API
const (
	ecsSettingEnabled  = "enabled"
	ecsSettingDisabled = "disabled"
)

const (
	ParameterKeyAsgMaxSize               = "AsgMaxSize"
	ParameterKeyVPCAzs                   = "VpcAvailabilityZones"
	ParameterKeySecurityGroup            = "SecurityGroupIds"
	ParameterKeySourceCidr               = "SourceCidr"
	ParameterKeyEcsPort                  = "EcsPort"
	ParameterKeySubnetIds                = "SubnetIds"
	ParameterKeyVpcId                    = "VpcId"
	ParameterKeyInstanceType             = "EcsInstanceType"
	ParameterKeyKeyPairName              = "KeyName"
	ParameterKeyCluster                  = "EcsCluster"
	ParameterKeyAmiId                    = "EcsAmiId"
	ParameterKeyAssociatePublicIPAddress = "AssociatePublicIpAddress"
	ParameterKeyIsIMDSv2                 = "IsIMDSv2"
	ParameterKeyInstanceRole             = "InstanceRole"
	ParameterKeyIsFargate                = "IsFargate"
	ParameterKeyUserData                 = "UserData"
	ParameterKeySpotPrice                = "SpotPrice"
)

const (
	invalidInstanceTypeFmt     = "instance type %s not found in list of supported instance types %s"
	instanceTypeUnsupportedFmt = "instance type %s not supported in region %s: %w"
)

var flagNamesToStackParameterKeys map[string]string
var requiredParameters []string = []string{ParameterKeyCluster}

func init() {
	flagNamesToStackParameterKeys = map[string]string{
		flags.AsgMaxSizeFlag:    ParameterKeyAsgMaxSize,
		flags.VpcAzFlag:         ParameterKeyVPCAzs,
		flags.SecurityGroupFlag: ParameterKeySecurityGroup,
		flags.SourceCidrFlag:    ParameterKeySourceCidr,
		flags.EcsPortFlag:       ParameterKeyEcsPort,
		flags.SubnetIdsFlag:     ParameterKeySubnetIds,
		flags.VpcIdFlag:         ParameterKeyVpcId,
		flags.InstanceTypeFlag:  ParameterKeyInstanceType,
		flags.KeypairNameFlag:   ParameterKeyKeyPairName,
		flags.ImageIdFlag:       ParameterKeyAmiId,
		flags.InstanceRoleFlag:  ParameterKeyInstanceRole,
		flags.SpotPriceFlag:     ParameterKeySpotPrice,
	}
}

type AWSClients struct {
	ECSClient         ecsclient.ECSClient
	CFNClient         cloudformation.CloudformationClient
	AMIMetadataClient amimetadata.Client
	EC2Client         ec2client.EC2Client
}

func newAWSClients(commandConfig *config.CommandConfig) *AWSClients {
	ecsClient := ecsclient.NewECSClient(commandConfig)
	cfnClient := cloudformation.NewCloudformationClient(commandConfig)
	metadataClient := amimetadata.NewMetadataClient(commandConfig)
	ec2Client := ec2client.NewEC2Client(commandConfig)

	return &AWSClients{ecsClient, cfnClient, metadataClient, ec2Client}
}

// /////////////////////
// Public Functions //
// ///////////////////
func ClusterUp(c *cli.Context) {
	rdwr, err := config.NewReadWriter()
	if err != nil {
		logrus.Fatal("Error executing 'up': ", err)
	}

	commandConfig, err := newCommandConfig(c, rdwr)
	if err != nil {
		logrus.Fatal("Error executing 'up': ", err)
	}

	awsClients := newAWSClients(commandConfig)

	err = createCluster(c, awsClients, commandConfig)
	if err != nil {
		logrus.Fatal("Error executing 'up': ", err)
	}

	if !c.Bool(flags.EmptyFlag) {
		// Displays resources create by CloudFormation, as a convenience for tasks launched
		// with Task Networking or in Fargate mode.
		if err := awsClients.CFNClient.DescribeNetworkResources(commandConfig.CFNStackName); err != nil {
			logrus.Error("Error describing Cloudformation resources: ", err)
		}
	}

	fmt.Println("Cluster creation succeeded.")
}

func ClusterDown(c *cli.Context) {
	rdwr, err := config.NewReadWriter()
	if err != nil {
		logrus.Fatal("Error executing 'down': ", err)
	}

	commandConfig, err := newCommandConfig(c, rdwr)
	if err != nil {
		logrus.Fatal("Error executing 'down': ", err)
	}

	awsClients := newAWSClients(commandConfig)

	if err := deleteCluster(c, awsClients, commandConfig); err != nil {
		logrus.Fatal("Error executing 'down': ", err)
	}
}

func ClusterScale(c *cli.Context) {
	rdwr, err := config.NewReadWriter()
	if err != nil {
		logrus.Fatal("Error executing 'scale': ", err)
	}

	commandConfig, err := newCommandConfig(c, rdwr)
	if err != nil {
		logrus.Fatal("Error executing 'scale': ", err)
	}

	awsClients := newAWSClients(commandConfig)

	if err := scaleCluster(c, awsClients, commandConfig); err != nil {
		logrus.Fatal("Error executing 'scale': ", err)
	}
}

func ClusterPS(c *cli.Context) {
	rdwr, err := config.NewReadWriter()
	if err != nil {
		logrus.Fatal("Error executing 'ps': ", err)
	}

	infoSet, err := clusterPS(c, rdwr)
	if err != nil {
		logrus.Fatal("Error executing 'ps': ", err)
	}
	os.Stdout.WriteString(infoSet.String(container.ContainerInfoColumns, displayTitle))
}

///////////////////////
// Helper functions //
//////////////////////

// createCluster executes the 'up' command.
func createCluster(context *cli.Context, awsClients *AWSClients, commandConfig *config.CommandConfig) error {
	var err error

	ecsClient := awsClients.ECSClient
	cfnClient := awsClients.CFNClient
	metadataClient := awsClients.AMIMetadataClient

	// Check if cluster is specified
	if commandConfig.Cluster == "" {
		return clusterNotSetError()
	}

	if context.Bool(flags.EmptyFlag) {
		err = createEmptyCluster(context, ecsClient, cfnClient, commandConfig)
		if err != nil {
			return err
		}
		return nil
	}

	launchType := commandConfig.LaunchType
	if launchType == "" {
		launchType = config.LaunchTypeDefault
	}

	// InstanceRole not needed when creating empty cluster for Fargate tasks
	if launchType == config.LaunchTypeEC2 {
		if err := validateInstanceRole(context); err != nil {
			return err
		}
		// Display warning if keypair not specified
		if context.String(flags.KeypairNameFlag) == "" {
			logrus.Warn("You will not be able to SSH into your EC2 instances without a key pair.")
		}

	}

	// Check if cfn stack already exists
	stackName := commandConfig.CFNStackName
	var deleteStack bool
	if err = cfnClient.ValidateStackExists(stackName); err == nil {
		if !isForceSet(context) {
			return fmt.Errorf("A CloudFormation stack already exists for the cluster '%s'. Please specify '--%s' to clean up your existing resources", commandConfig.Cluster, flags.ForceFlag)
		}
		deleteStack = true
	}

	tags := make([]*ecs.Tag, 0)
	if tagVal := context.String(flags.ResourceTagsFlag); tagVal != "" {
		tags, err = utils.ParseTags(tagVal, tags)
		if err != nil {
			return err
		}
	}

	var containerInstanceTaggingSupported bool

	if len(tags) > 0 {
		// determine if container instance tagging is supported
		containerInstanceTaggingSupported, err = canEnableContainerInstanceTagging(awsClients.ECSClient)
		if err != nil {
			return err
		}
	}

	// Populate cfn params
	var cfnParams *cloudformation.CfnStackParams
	if containerInstanceTaggingSupported {
		cfnParams, err = cliFlagsToCfnStackParams(context, commandConfig.Cluster, launchType, tags)
	} else {
		cfnParams, err = cliFlagsToCfnStackParams(context, commandConfig.Cluster, launchType, nil)
	}
	if err != nil {
		return err
	}

	cfnParams.Add(ParameterKeyCluster, commandConfig.Cluster)
	if context.Bool(flags.NoAutoAssignPublicIPAddressFlag) {
		cfnParams.Add(ParameterKeyAssociatePublicIPAddress, "false")
	}

	if context.Bool(flags.IMDSv2Flag) {
		cfnParams.Add(ParameterKeyIsIMDSv2, "true")
	}

	if launchType == config.LaunchTypeFargate {
		cfnParams.Add(ParameterKeyIsFargate, "true")
	}

	// Check if vpc and AZs are not both specified.
	if validateMutuallyExclusiveParams(cfnParams, ParameterKeyVPCAzs, ParameterKeyVpcId) {
		return fmt.Errorf("You can only specify '--%s' or '--%s'", flags.VpcIdFlag, flags.VpcAzFlag)
	}

	// Check that user data is not specified with Fargate
	if validateMutuallyExclusiveParams(cfnParams, ParameterKeyIsFargate, ParameterKeyUserData) {
		return fmt.Errorf("You can only specify '--%s' with the EC2 launch type", flags.UserDataFlag)
	}

	// Check if 2 AZs are specified
	if validateCommaSeparatedParam(cfnParams, ParameterKeyVPCAzs, 2, 2) {
		return fmt.Errorf("You must specify 2 comma-separated availability zones with the '--%s' flag", flags.VpcAzFlag)
	}

	// Check if more than one custom instance role is specified
	if validateCommaSeparatedParam(cfnParams, ParameterKeyInstanceRole, 1, 1) {
		return fmt.Errorf("You can only specify one instance role name with the '--%s' flag", flags.InstanceRoleFlag)
	}

	// Check if vpc exists when security group is specified
	if validateDependentParams(cfnParams, ParameterKeySecurityGroup, ParameterKeyVpcId) {
		return fmt.Errorf("You have selected a security group. Please specify a VPC with the '--%s' flag", flags.VpcIdFlag)
	}

	// Check if subnets exists when vpc is specified
	if validateDependentParams(cfnParams, ParameterKeyVpcId, ParameterKeySubnetIds) {
		return fmt.Errorf("You have selected a VPC. Please specify 2 comma-separated subnets with the '--%s' flag", flags.SubnetIdsFlag)
	}

	// Check if vpc exists when subnets is specified
	if validateDependentParams(cfnParams, ParameterKeySubnetIds, ParameterKeyVpcId) {
		return fmt.Errorf("You have selected subnets. Please specify a VPC with the '--%s' flag", flags.VpcIdFlag)
	}

	if launchType == config.LaunchTypeEC2 {
		instanceType, err := getInstanceType(cfnParams)
		if err != nil {
			return err
		}
		supportedInstanceTypes, err := awsClients.EC2Client.DescribeInstanceTypeOfferings(commandConfig.Region())
		if err != nil {
			return fmt.Errorf("describe instance type offerings: %w", err)
		}

		if err = validateInstanceType(instanceType, supportedInstanceTypes); err != nil {
			// if we detect the default value is unsupported then we'll suggest to the user overriding the value with the appropriate flag
			if instanceType == cloudformation.DefaultECSInstanceType {
				logrus.Warnf("Default instance type %s not supported in region %s. Override the default instance type with the --%s flag and provide a supported value.",
					instanceType, commandConfig.Region(), flags.InstanceTypeFlag)
			}
			return fmt.Errorf(instanceTypeUnsupportedFmt, instanceType, commandConfig.Region(), err)
		}

		// Check if image id was supplied, else populate
		_, err = cfnParams.GetParameter(ParameterKeyAmiId)
		if err == cloudformation.ParameterNotFoundError {
			err := populateAMIID(cfnParams, metadataClient)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	if err := cfnParams.Validate(); err != nil {
		return err
	}

	// Create ECS cluster
	if _, err := ecsClient.CreateCluster(commandConfig.Cluster, tags); err != nil {
		return err
	}

	// Delete cfn stack
	if deleteStack {
		if err := cfnClient.DeleteStack(stackName); err != nil {
			return err
		}
		logrus.Info("Waiting for your CloudFormation stack resources to be deleted...")
		if err := cfnClient.WaitUntilDeleteComplete(stackName); err != nil {
			return err
		}
	}
	// Create cfn stack
	template, err := cloudformation.GetClusterTemplate(tags, stackName)
	if err != nil {
		return errors.Wrapf(err, "Error building cloudformation template")
	}

	if _, err := cfnClient.CreateStack(template, stackName, true, cfnParams, convertToCFNTags(tags)); err != nil {
		return err
	}

	logrus.Info("Waiting for your cluster resources to be created...")
	// Wait for stack creation
	return cfnClient.WaitUntilCreateComplete(stackName)
}

func canEnableContainerInstanceTagging(client ecsclient.ECSClient) (bool, error) {
	output, err := client.ListAccountSettings(&ecs.ListAccountSettingsInput{
		EffectiveSettings: aws.Bool(true),
		Name:              aws.String(ecs.SettingNameContainerInstanceLongArnFormat),
	})
	if err != nil {
		return false, err
	}

	// This should never evaluate to true, unless there is a problem with API
	// This if block ensures that the CLI does not panic in that case
	if len(output.Settings) < 1 {
		return false, fmt.Errorf("Received unexpected response from ECS Settings API: %s", output)
	}

	if aws.StringValue(output.Settings[0].Value) == ecsSettingEnabled {
		logrus.Warnf("Enabling container instance tagging because %s is enabled for your identity, %s. If this is not your account default setting, your instances will fail to join your cluster. You can use the PutAccountSettingDefault API to change your account default.", ecs.SettingNameContainerInstanceLongArnFormat, aws.StringValue(output.Settings[0].PrincipalArn))
		return true, nil
	}

	logrus.Warnf("Disabling container instance tagging because %s is not enabled for your identity, %s. You can use the PutAccountSettingDefault API to change your account default.", ecs.SettingNameContainerInstanceLongArnFormat, aws.StringValue(output.Settings[0].PrincipalArn))
	return false, nil
}

func getInstanceType(cfnParams *cloudformation.CfnStackParams) (string, error) {
	param, err := cfnParams.GetParameter(ParameterKeyInstanceType)
	if err == cloudformation.ParameterNotFoundError {
		logrus.Infof("Defaulting instance type to %s", cloudformation.DefaultECSInstanceType)

		cfnParams.Add(ParameterKeyInstanceType, cloudformation.DefaultECSInstanceType)

		return cloudformation.DefaultECSInstanceType, nil
	} else if err != nil {
		return "", err
	}

	return aws.StringValue(param.ParameterValue), nil
}

func validateInstanceType(instanceType string, supportedInstanceTypes []string) error {
	found := false
	for _, it := range supportedInstanceTypes {
		if it == instanceType {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf(invalidInstanceTypeFmt, instanceType, supportedInstanceTypes)
	}

	return nil
}

func populateAMIID(cfnParams *cloudformation.CfnStackParams, client amimetadata.Client) error {
	instanceType, err := getInstanceType(cfnParams)
	if err != nil {
		return err
	}

	amiMetadata, err := client.GetRecommendedECSLinuxAMI(instanceType)
	if err != nil {
		return err
	}
	logrus.Infof("Using recommended %s AMI with ECS Agent %s and %s",
		amiMetadata.OsName, amiMetadata.AgentVersion, amiMetadata.RuntimeVersion)
	cfnParams.Add(ParameterKeyAmiId, amiMetadata.ImageID)
	return nil
}

// unfortunately go SDK lacks a unified Tag type
func convertToCFNTags(tags []*ecs.Tag) []*sdkCFN.Tag {
	var cfnTags []*sdkCFN.Tag
	for _, tag := range tags {
		cfnTags = append(cfnTags, &sdkCFN.Tag{
			Key:   tag.Key,
			Value: tag.Value,
		})
	}
	return cfnTags
}

var newCommandConfig = func(context *cli.Context, rdwr config.ReadWriter) (*config.CommandConfig, error) {
	return config.NewCommandConfig(context, rdwr)
}

func createEmptyCluster(context *cli.Context, ecsClient ecsclient.ECSClient, cfnClient cloudformation.CloudformationClient, commandConfig *config.CommandConfig) error {
	for _, flag := range flags.CFNResourceFlags() {
		if context.String(flag) != "" {
			logrus.Warnf("Value for flag '%v' will be ignored when creating an empty cluster", flag)
		}
	}
	if isIAMAcknowledged(context) {
		logrus.Warnf("The '--%v' flag will be ignored when creating an empty cluster", flags.CapabilityIAMFlag)
	}

	if isForceSet(context) {
		logrus.Warn("Force flag is unsupported when creating an empty cluster.")
	}

	// Check if non-empty cluster with same name already exists
	stackName := commandConfig.CFNStackName
	if err := cfnClient.ValidateStackExists(stackName); err == nil {
		return fmt.Errorf("A CloudFormation stack already exists for the cluster '%s'.", commandConfig.Cluster)
	}

	tags := make([]*ecs.Tag, 0)
	var err error
	if tagVal := context.String(flags.ResourceTagsFlag); tagVal != "" {
		tags, err = utils.ParseTags(tagVal, tags)
		if err != nil {
			return err
		}
	}

	if _, err := ecsClient.CreateCluster(commandConfig.Cluster, tags); err != nil {
		return err
	}

	return nil
}

var deleteCFNStack = func(cfnClient cloudformation.CloudformationClient, commandConfig *config.CommandConfig) error {
	stackName := commandConfig.CFNStackName
	if err := cfnClient.DeleteStack(stackName); err != nil {
		return err
	}

	logrus.Info("Waiting for your cluster resources to be deleted...")
	if err := cfnClient.WaitUntilDeleteComplete(stackName); err != nil {
		return err
	}

	return nil
}

// deleteCluster executes the 'down' command.
func deleteCluster(context *cli.Context, awsClients *AWSClients, commandConfig *config.CommandConfig) error {
	// Validate cli flags
	if !isForceSet(context) {
		reader := bufio.NewReader(os.Stdin)
		if err := deleteClusterPrompt(reader); err != nil {
			return err
		}
	}

	// Validate that cluster exists in ECS
	ecsClient := awsClients.ECSClient
	if err := validateCluster(commandConfig.Cluster, ecsClient); err != nil {
		return err
	}

	// Validate that a cfn stack exists for the cluster
	cfnClient := awsClients.CFNClient
	stackName := commandConfig.CFNStackName

	if err := cfnClient.ValidateStackExists(stackName); err != nil {
		logrus.Infof("No CloudFormation stack found for cluster '%s'.", commandConfig.Cluster)
	} else {
		if err := deleteCFNStack(cfnClient, commandConfig); err != nil {
			return err
		}
	}

	// Delete cluster in ECS
	if _, err := ecsClient.DeleteCluster(commandConfig.Cluster); err != nil {
		return err
	}

	return nil
}

// scaleCluster executes the 'scale' command.
func scaleCluster(context *cli.Context, awsClients *AWSClients, commandConfig *config.CommandConfig) error {
	// Validate cli flags
	if !isIAMAcknowledged(context) {
		return fmt.Errorf("Please acknowledge that this command may create IAM resources with the '--%s' flag", flags.CapabilityIAMFlag)
	}

	size, err := getClusterSize(context)
	if err != nil {
		return err
	}
	if size == "" {
		return fmt.Errorf("Missing required flag '--%s'", flags.AsgMaxSizeFlag)
	}

	// Validate that cluster exists in ECS
	ecsClient := awsClients.ECSClient
	if err := validateCluster(commandConfig.Cluster, ecsClient); err != nil {
		return err
	}

	// Validate that we have a cfn stack for the cluster
	cfnClient := awsClients.CFNClient
	stackName := commandConfig.CFNStackName
	existingParameters, err := cfnClient.GetStackParameters(stackName)
	if err != nil {
		return fmt.Errorf("CloudFormation stack not found for cluster '%s'", commandConfig.Cluster)
	}

	// Populate update params for the cfn stack
	cfnParams, err := cloudformation.NewCfnStackParamsForUpdate(requiredParameters, existingParameters)
	if err != nil {
		return err
	}
	cfnParams.Add(ParameterKeyAsgMaxSize, size)

	// Update the stack.
	if _, err := cfnClient.UpdateStack(stackName, cfnParams); err != nil {
		return err
	}

	logrus.Info("Waiting for your cluster resources to be updated...")
	return cfnClient.WaitUntilUpdateComplete(stackName)
}

// createPS executes the 'ps' command.
func clusterPS(context *cli.Context, rdwr config.ReadWriter) (project.InfoSet, error) {
	commandConfig, err := newCommandConfig(context, rdwr)
	if err != nil {
		return nil, err
	}

	// Validate that cluster exists in ECS
	ecsClient := ecsclient.NewECSClient(commandConfig)
	if err := validateCluster(commandConfig.Cluster, ecsClient); err != nil {
		return nil, err
	}
	ec2Client := ec2client.NewEC2Client(commandConfig)

	ecsContext := &ecscontext.ECSContext{ECSClient: ecsClient, EC2Client: ec2Client}
	task := task.NewTask(ecsContext)
	return task.Info(false, context.String(flags.DesiredTaskStatus))
}

// validateCluster validates if the cluster exists in ECS and is in "ACTIVE" state.
func validateCluster(clusterName string, ecsClient ecsclient.ECSClient) error {
	if clusterName == "" {
		return clusterNotSetError()
	}
	isClusterActive, err := ecsClient.IsActiveCluster(clusterName)
	if err != nil {
		return err
	}

	if !isClusterActive {
		return fmt.Errorf("Cluster '%s' is not active. Ensure that it exists", clusterName)
	}
	return nil
}

// deleteClusterPrompt prompts and checks for confirmation to delete the cluster
func deleteClusterPrompt(reader *bufio.Reader) error {
	fmt.Println("Are you sure you want to delete your cluster? [y/N]")
	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("Error reading input: %s", err.Error())
	}
	formattedInput := strings.ToLower(strings.TrimSpace(input))
	if formattedInput != "yes" && formattedInput != "y" {
		return fmt.Errorf("Aborted cluster deletion. To delete your cluster, re-run this command and specify the '--%s' flag or confirm that you'd like to delete your cluster at the prompt.", flags.ForceFlag)
	}
	return nil
}

// cliFlagsToCfnStackParams converts values set for CLI flags to cloudformation stack parameters.
func cliFlagsToCfnStackParams(context *cli.Context, cluster, launchType string, tags []*ecs.Tag) (*cloudformation.CfnStackParams, error) {
	cfnParams := cloudformation.NewCfnStackParams(requiredParameters)
	for cliFlag, cfnParamKeyName := range flagNamesToStackParameterKeys {
		cfnParamKeyValue := context.String(cliFlag)
		if cfnParamKeyValue != "" {
			cfnParams.Add(cfnParamKeyName, cfnParamKeyValue)
		}
	}

	if launchType == config.LaunchTypeEC2 {
		builder := newUserDataBuilder(cluster, tags)
		// handle extra user data, which is a string slice flag
		if userDataFiles := context.StringSlice(flags.UserDataFlag); len(userDataFiles) > 0 {
			for _, file := range userDataFiles {
				err := builder.AddFile(file)
				if err != nil {
					return nil, err
				}
			}
		}
		userData, err := builder.Build()
		if err != nil {
			return nil, err
		}
		cfnParams.Add(ParameterKeyUserData, userData)
	}
	return cfnParams, nil
}

// isIAMAcknowledged returns true if the 'capability-iam' flag is set from CLI.
func isIAMAcknowledged(context *cli.Context) bool {
	return context.Bool(flags.CapabilityIAMFlag)
}

// returns true if customer specifies a custom instance role via 'role' flag.
func hasCustomRole(context *cli.Context) bool {
	return context.String(flags.InstanceRoleFlag) != "" // validate arn?
}

func validateInstanceRole(context *cli.Context) error {
	defaultRole := isIAMAcknowledged(context)
	customRole := hasCustomRole(context)

	if !defaultRole && !customRole {
		return fmt.Errorf("You must either specify a custom role with the '--%s' flag or set the '--%s' flag", flags.InstanceRoleFlag, flags.CapabilityIAMFlag)
	}
	if defaultRole && customRole {
		return fmt.Errorf("Cannot specify custom role when '--%s' flag is set", flags.CapabilityIAMFlag)
	}
	return nil
}

// isForceSet returns true if the 'force' flag is set from CLI.
func isForceSet(context *cli.Context) bool {
	return context.Bool(flags.ForceFlag)
}

// clusterNotSetError recommends that users either configure or provide a cluster flag
func clusterNotSetError() error {
	return fmt.Errorf("Please configure a cluster using the configure command or the '--%s' flag", flags.ClusterFlag)
}

// getClusterSize gets the value for the 'size' flag from CLI.
func getClusterSize(context *cli.Context) (string, error) {
	size := context.String(flags.AsgMaxSizeFlag)
	if size != "" {
		if _, err := strconv.Atoi(size); err != nil {
			return "", err
		}
	}

	return size, nil
}

// If param1 exists, param2 is not allowed.
func validateMutuallyExclusiveParams(cfnParams *cloudformation.CfnStackParams, param1, param2 string) bool {
	if _, err := cfnParams.GetParameter(param1); err != nil {
		return false
	}
	if _, err := cfnParams.GetParameter(param2); err != cloudformation.ParameterNotFoundError {
		return true
	}
	return false
}

// If param1 exists, param2 is required.
func validateDependentParams(cfnParams *cloudformation.CfnStackParams, param1, param2 string) bool {
	if _, err := cfnParams.GetParameter(param1); err != nil {
		return false
	}
	if _, err := cfnParams.GetParameter(param2); err == cloudformation.ParameterNotFoundError {
		return true
	}
	return false
}

func validateCommaSeparatedParam(cfnParams *cloudformation.CfnStackParams, param string, minLength, maxLength int) bool {
	values, err := cfnParams.GetParameter(param)
	if err != nil {
		return false
	}
	if splitValues := strings.Split(*values.ParameterValue, ","); len(splitValues) < minLength || len(splitValues) > maxLength {
		return true
	}
	return false
}
