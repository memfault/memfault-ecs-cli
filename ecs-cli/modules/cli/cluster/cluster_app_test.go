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
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/cli/cluster/userdata"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/amimetadata"
	mock_amimetadata "github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/amimetadata/mock"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/cloudformation"
	mock_cloudformation "github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/cloudformation/mock"
	mock_ec2 "github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/ec2/mock"
	mock_ecs "github.com/aws/amazon-ecs-cli/ecs-cli/modules/clients/aws/ecs/mock"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/commands/flags"
	"github.com/aws/amazon-ecs-cli/ecs-cli/modules/config"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	sdkCFN "github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli"
)

const (
	clusterName    = "defaultCluster"
	stackName      = "defaultCluster"
	amiID          = "ami-deadb33f"
	armAMIID       = "ami-baadf00d"
	mockedUserData = "some user data"
)

type mockReadWriter struct {
	clusterName       string
	stackName         string
	defaultLaunchType string
}

func (rdwr *mockReadWriter) Get(cluster string, profile string) (*config.LocalConfig, error) {
	cliConfig := config.NewLocalConfig(rdwr.clusterName)
	cliConfig.CFNStackName = rdwr.clusterName
	cliConfig.DefaultLaunchType = rdwr.defaultLaunchType
	return cliConfig, nil
}

func (rdwr *mockReadWriter) SaveProfile(configName string, profile *config.Profile) error {
	return nil
}

func (rdwr *mockReadWriter) SaveCluster(configName string, cluster *config.Cluster) error {
	return nil
}

func (rdwr *mockReadWriter) SetDefaultProfile(configName string) error {
	return nil
}

func (rdwr *mockReadWriter) SetDefaultCluster(configName string) error {
	return nil
}

func newMockReadWriter() *mockReadWriter {
	return &mockReadWriter{
		clusterName: clusterName,
	}
}

type mockUserDataBuilder struct {
	userdata string
	files    []string
	tags     []*ecs.Tag
}

func (b *mockUserDataBuilder) AddFile(fileName string) error {
	b.files = append(b.files, fileName)
	return nil
}

func (b *mockUserDataBuilder) Build() (string, error) {
	return b.userdata, nil
}

func setupTest(t *testing.T) (*mock_ecs.MockECSClient, *mock_cloudformation.MockCloudformationClient, *mock_amimetadata.MockClient, *mock_ec2.MockEC2Client) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockECS := mock_ecs.NewMockECSClient(ctrl)
	mockCloudformation := mock_cloudformation.NewMockCloudformationClient(ctrl)
	mockSSM := mock_amimetadata.NewMockClient(ctrl)
	mockEC2 := mock_ec2.NewMockEC2Client(ctrl)

	os.Setenv("AWS_ACCESS_KEY", "AKIDEXAMPLE")
	os.Setenv("AWS_SECRET_KEY", "secret")
	os.Setenv("AWS_REGION", "us-west-1")

	return mockECS, mockCloudformation, mockSSM, mockEC2
}

/////////////////
// Cluster Up //
////////////////

func TestClusterUp(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	mocksForSuccessfulClusterUp(mockECS, mockCloudformation, mockSSM, mockEC2)

	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithForce(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)

	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("t2.micro").Return(amiMetadata(amiID), nil),
	)

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(nil),
		mockCloudformation.EXPECT().DeleteStack(stackName).Return(nil),
		mockCloudformation.EXPECT().WaitUntilDeleteComplete(stackName).Return(nil),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)

	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.ForceFlag, true, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithoutPublicIP(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)

	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("t2.micro").Return(amiMetadata(amiID), nil),
	)

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			capabilityIAM := x.(bool)
			cfnParams := y.(*cloudformation.CfnStackParams)
			associateIPAddress, err := cfnParams.GetParameter(ParameterKeyAssociatePublicIPAddress)
			assert.NoError(t, err, "Unexpected error getting cfn parameter")
			assert.Equal(t, "false", aws.StringValue(associateIPAddress.ParameterValue), "Should not associate public IP address")
			assert.True(t, capabilityIAM, "Expected capability capabilityIAM to be true")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)

	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)

	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.NoAutoAssignPublicIPAddressFlag, true, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithUserData(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	oldNewUserDataBuilder := newUserDataBuilder
	defer func() { newUserDataBuilder = oldNewUserDataBuilder }()
	userdataMock := &mockUserDataBuilder{
		userdata: mockedUserData,
	}
	newUserDataBuilder = func(clusterName string, tags []*ecs.Tag) userdata.UserDataBuilder {
		userdataMock.tags = tags
		return userdataMock
	}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)

	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("t2.micro").Return(amiMetadata(amiID), nil),
	)

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			cfnParams := y.(*cloudformation.CfnStackParams)
			param, err := cfnParams.GetParameter(ParameterKeyUserData)
			assert.NoError(t, err, "Expected User Data parameter to be set")
			assert.Equal(t, mockedUserData, aws.StringValue(param.ParameterValue), "Expected user data to match")
			assert.Nil(t, userdataMock.tags, "Expected container instance tagging to be disabled")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)

	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)

	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	userDataFiles := &cli.StringSlice{}
	userDataFiles.Set("some_file")
	userDataFiles.Set("some_file2")
	flagSet.Var(userDataFiles, flags.UserDataFlag, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")

	assert.ElementsMatch(t, []string{"some_file", "some_file2"}, userdataMock.files, "Expected userdata file list to match")
}

func TestClusterUpWithSpotPrice(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	spotPrice := "0.03"

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)

	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("t2.micro").Return(amiMetadata(amiID), nil),
	)

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			cfnParams := y.(*cloudformation.CfnStackParams)
			param, err := cfnParams.GetParameter(ParameterKeySpotPrice)
			assert.NoError(t, err, "Expected Spot Price parameter to be set")
			assert.Equal(t, spotPrice, aws.StringValue(param.ParameterValue), "Expected spot price to match")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)

	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)

	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.SpotPriceFlag, spotPrice, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithVPC(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	vpcID := "vpc-02dd3038"
	subnetIds := "subnet-04726b21,subnet-04346b21"

	mocksForSuccessfulClusterUp(mockECS, mockCloudformation, mockSSM, mockEC2)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.VpcIdFlag, vpcID, "")
	flagSet.String(flags.SubnetIdsFlag, subnetIds, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithAvailabilityZones(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	vpcAZs := "us-west-2c,us-west-2a"

	mocksForSuccessfulClusterUp(mockECS, mockCloudformation, mockSSM, mockEC2)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.VpcAzFlag, vpcAZs, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithCustomRole(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	instanceRole := "sparklepony"

	mocksForSuccessfulClusterUp(mockECS, mockCloudformation, mockSSM, mockEC2)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.InstanceRoleFlag, instanceRole, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithTwoCustomRoles(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	instanceRole := "sparklepony, sparkleunicorn"

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.InstanceRoleFlag, instanceRole, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for custom instance role")
}

func TestClusterUpWithDefaultAndCustomRoles(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	instanceRole := "sparklepony"

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.InstanceRoleFlag, instanceRole, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for custom instance role")
}

func TestClusterUpWithNoRoles(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, false, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for custom instance role")
}

func TestClusterUpWithoutKeyPair(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	mocksForSuccessfulClusterUp(mockECS, mockCloudformation, mockSSM, mockEC2)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.Bool(flags.ForceFlag, true, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithSecurityGroupWithoutVPC(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	securityGroupID := "sg-eeaabc8d"

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.ForceFlag, true, "")
	flagSet.String(flags.SecurityGroupFlag, securityGroupID, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for security group without VPC")
}

func TestClusterUpWith2SecurityGroups(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)

	mocksForSuccessfulClusterUp(mockECS, mockCloudformation, mockSSM, mockEC2)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	securityGroupIds := "sg-eeaabc8d,sg-eaaebc8d"
	vpcId := "vpc-02dd3038"
	subnetIds := "subnet-04726b21,subnet-04346b21"

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.ForceFlag, true, "")
	flagSet.String(flags.SecurityGroupFlag, securityGroupIds, "")
	flagSet.String(flags.VpcIdFlag, vpcId, "")
	flagSet.String(flags.SubnetIdsFlag, subnetIds, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithSubnetsWithoutVPC(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	subnetID := "subnet-72f52e32"

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.ForceFlag, true, "")
	flagSet.String(flags.SubnetIdsFlag, subnetID, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for subnets without VPC")
}

func TestClusterUpWithVPCWithoutSubnets(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	vpcID := "vpc-02dd3038"

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.ForceFlag, true, "")
	flagSet.String(flags.VpcIdFlag, vpcID, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for VPC without subnets")
}

func TestClusterUpWithAvailabilityZonesWithVPC(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	vpcID := "vpc-02dd3038"
	vpcAZs := "us-west-2c,us-west-2a"

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.ForceFlag, true, "")
	flagSet.String(flags.VpcIdFlag, vpcID, "")
	flagSet.String(flags.VpcAzFlag, vpcAZs, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for VPC with AZs")
}

func TestClusterUpWithout2AvailabilityZones(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	vpcAZs := "us-west-2c"

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.Bool(flags.ForceFlag, true, "")
	flagSet.String(flags.VpcAzFlag, vpcAZs, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error for 2 AZs")
}

func TestCliFlagsToCfnStackParams(t *testing.T) {

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")

	context := cli.NewContext(nil, flagSet, nil)
	params, err := cliFlagsToCfnStackParams(context, clusterName, config.LaunchTypeEC2, nil)
	assert.NoError(t, err, "Unexpected error from call to cliFlagsToCfnStackParams")

	_, err = params.GetParameter(ParameterKeyAsgMaxSize)
	assert.Error(t, err, "Expected error for parameter ParameterKeyAsgMaxSize")
	assert.Equal(t, cloudformation.ParameterNotFoundError, err, "Expect error to be ParameterNotFoundError")

	flagSet.String(flags.AsgMaxSizeFlag, "2", "")
	context = cli.NewContext(nil, flagSet, nil)
	params, err = cliFlagsToCfnStackParams(context, clusterName, config.LaunchTypeEC2, nil)
	assert.NoError(t, err, "Unexpected error from call to cliFlagsToCfnStackParams")
	_, err = params.GetParameter(ParameterKeyAsgMaxSize)
	assert.NoError(t, err, "Unexpected error getting parameter ParameterKeyAsgMaxSize")
}

func TestClusterUpForImageIdInput_And_IMDSv2(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	imageID := "ami-12345"

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)

	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("x86").Return(amiMetadata(imageID), nil),
	)

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			capabilityIAM := x.(bool)
			cfnStackParams := y.(*cloudformation.CfnStackParams)
			actualAMIID, err := cfnStackParams.GetParameter(ParameterKeyAmiId)
			assert.NoError(t, err, "Expected image id params to be present")
			actualIsIMDSv2, err := cfnStackParams.GetParameter(ParameterKeyIsIMDSv2)
			assert.NoError(t, err, "Expected IsIMDSv2 parameter to be present")

			assert.Equal(t, imageID, aws.StringValue(actualAMIID.ParameterValue), "Expected image id to match")
			assert.True(t, capabilityIAM, "Expected capability capabilityIAM to be true")
			assert.Equal(t, "true", aws.StringValue(actualIsIMDSv2.ParameterValue), "Expected IMDS v2 to be enabled")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)

	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.ImageIdFlag, imageID, "")
	flagSet.Bool(flags.IMDSv2Flag, true, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithClusterNameEmpty(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")

	context := cli.NewContext(nil, flagSet, globalContext)
	rdwr := &mockReadWriter{clusterName: ""}
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error bringing up cluster")
}

func TestClusterUpWithoutRegion(t *testing.T) {
	defer os.Clearenv()
	os.Unsetenv("AWS_REGION")

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	_, err := newCommandConfig(context, rdwr)
	assert.Error(t, err, "Expected error due to missing region in bringing up cluster")
}

func TestClusterUpWithFargateLaunchTypeFlag(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("x86").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			cfnParams := y.(*cloudformation.CfnStackParams)
			isFargate, err := cfnParams.GetParameter(ParameterKeyIsFargate)
			assert.NoError(t, err, "Unexpected error getting cfn parameter")
			assert.Equal(t, "true", aws.StringValue(isFargate.ParameterValue), "Should have Fargate launch type.")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
		mockCloudformation.EXPECT().DescribeNetworkResources(stackName).Return(nil),
	)
	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)
	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.String(flags.LaunchTypeFlag, config.LaunchTypeFargate, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)

	assert.Equal(t, config.LaunchTypeFargate, commandConfig.LaunchType, "Launch Type should be FARGATE")
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithFargateDefaultLaunchTypeConfig(t *testing.T) {
	rdwr := &mockReadWriter{
		clusterName:       clusterName,
		defaultLaunchType: config.LaunchTypeFargate,
	}

	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			capabilityIAM := x.(bool)
			cfnParams := y.(*cloudformation.CfnStackParams)
			isFargate, err := cfnParams.GetParameter(ParameterKeyIsFargate)
			assert.NoError(t, err, "Unexpected error getting cfn parameter")
			assert.Equal(t, "true", aws.StringValue(isFargate.ParameterValue), "Should have Fargate launch type.")
			assert.True(t, capabilityIAM, "Expected capability capabilityIAM to be true")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
		mockCloudformation.EXPECT().DescribeNetworkResources(stackName).Return(nil),
	)
	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)
	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)

	assert.Equal(t, config.LaunchTypeFargate, commandConfig.LaunchType, "Launch Type should be FARGATE")
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithFargateLaunchTypeFlagOverride(t *testing.T) {
	rdwr := &mockReadWriter{
		clusterName:       clusterName,
		defaultLaunchType: config.LaunchTypeEC2,
	}

	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("x86").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			capabilityIAM := x.(bool)
			cfnParams := y.(*cloudformation.CfnStackParams)
			isFargate, err := cfnParams.GetParameter(ParameterKeyIsFargate)
			assert.NoError(t, err, "Unexpected error getting cfn parameter")
			assert.Equal(t, "true", aws.StringValue(isFargate.ParameterValue), "Should have Fargate launch type.")
			assert.True(t, capabilityIAM, "Expected capability capabilityIAM to be true")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
		mockCloudformation.EXPECT().DescribeNetworkResources(stackName).Return(nil),
	)
	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)
	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.String(flags.LaunchTypeFlag, config.LaunchTypeFargate, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)

	assert.Equal(t, config.LaunchTypeFargate, commandConfig.LaunchType, "Launch Type should be FARGATE")
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithEC2LaunchTypeFlagOverride(t *testing.T) {
	rdwr := &mockReadWriter{
		clusterName:       clusterName,
		defaultLaunchType: config.LaunchTypeFargate,
	}

	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("x86").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)
	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.String(flags.LaunchTypeFlag, config.LaunchTypeEC2, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)

	// This is kind of hack - this error will only get checked if launch type is EC2
	assert.Error(t, err, "Expected error for bringing up cluster with empty default launch type.")
}

func TestClusterUpWithBlankDefaultLaunchTypeConfig(t *testing.T) {
	rdwr := &mockReadWriter{
		clusterName:       clusterName,
		defaultLaunchType: "",
	}

	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)
	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, false, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)

	// This is kind of hack - this error will only get checked if launch type is EC2
	assert.Error(t, err, "Expected error for bringing up cluster with empty default launch type.")
}

func TestClusterUpWithEmptyCluster(t *testing.T) {
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("x86").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.EmptyFlag, true, "")
	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up empty cluster")
}

func TestClusterUpWithEmptyClusterWithExistingStack(t *testing.T) {
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("x86").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(nil),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.EmptyFlag, true, "")
	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Unexpected error bringing up empty cluster")
}

func TestClusterUpARM64(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)

	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("a1.medium").Return(amiMetadata(armAMIID), nil),
	)

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			capabilityIAM := x.(bool)
			cfnParams := y.(*cloudformation.CfnStackParams)
			amiIDParam, err := cfnParams.GetParameter(ParameterKeyAmiId)
			assert.NoError(t, err, "Unexpected error getting cfn parameter")
			assert.Equal(t, armAMIID, aws.StringValue(amiIDParam.ParameterValue), "Expected ami ID to be set to recommended for arm64")
			assert.True(t, capabilityIAM, "Expected capability capabilityIAM to be true")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)

	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro", "a1.medium"}, nil),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.InstanceTypeFlag, "a1.medium", "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithUnsupportedInstanceType(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	instanceType := "a1.medium"
	region := "us-west-1"
	supportedInstanceTypes := []string{"t2.micro"}

	invalidInstanceTypeErr := fmt.Errorf(invalidInstanceTypeFmt, instanceType, supportedInstanceTypes)
	expectedError := fmt.Errorf(instanceTypeUnsupportedFmt,
		instanceType, region, invalidInstanceTypeErr)

	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)

	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI(instanceType).Return(amiMetadata(armAMIID), nil),
	)

	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			capabilityIAM := x.(bool)
			cfnParams := y.(*cloudformation.CfnStackParams)
			amiIDParam, err := cfnParams.GetParameter(ParameterKeyAmiId)
			assert.NoError(t, err, "Unexpected error getting cfn parameter")
			assert.Equal(t, armAMIID, aws.StringValue(amiIDParam.ParameterValue), "Expected ami ID to be set to recommended for arm64")
			assert.True(t, capabilityIAM, "Expected capability capabilityIAM to be true")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)

	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings(region).Return(supportedInstanceTypes, nil),
	)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.KeypairNameFlag, "default", "")
	flagSet.String(flags.InstanceTypeFlag, instanceType, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.Equal(t, err, expectedError)
}

func TestClusterUpWithTags(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	expectedCFNTags := []*sdkCFN.Tag{
		&sdkCFN.Tag{
			Key:   aws.String("key"),
			Value: aws.String("peele"),
		},
		&sdkCFN.Tag{
			Key:   aws.String("mitchell"),
			Value: aws.String("webb"),
		},
	}

	expectedECSTags := []*ecs.Tag{
		&ecs.Tag{
			Key:   aws.String("key"),
			Value: aws.String("peele"),
		},
		&ecs.Tag{
			Key:   aws.String("mitchell"),
			Value: aws.String("webb"),
		},
	}

	listSettingsResponse := &ecs.ListAccountSettingsOutput{
		Settings: []*ecs.Setting{
			&ecs.Setting{
				Name:  aws.String(ecs.SettingNameContainerInstanceLongArnFormat),
				Value: aws.String("disabled"),
			},
		},
	}

	gomock.InOrder(
		mockECS.EXPECT().ListAccountSettings(gomock.Any()).Return(listSettingsResponse, nil),
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil).Do(func(x, y interface{}) {
			actualTags := y.([]*ecs.Tag)
			assert.ElementsMatch(t, expectedECSTags, actualTags, "Expected tags to match")
		}),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("t2.micro").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			actualTags := z.([]*sdkCFN.Tag)
			assert.ElementsMatch(t, expectedCFNTags, actualTags, "Expected tags to match")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
		mockCloudformation.EXPECT().DescribeNetworkResources(stackName).Return(nil),
	)
	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)
	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.String(flags.ResourceTagsFlag, "key=peele,mitchell=webb", "")
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")
}

func TestClusterUpWithTagsContainerInstanceTaggingEnabled(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	oldNewUserDataBuilder := newUserDataBuilder
	defer func() { newUserDataBuilder = oldNewUserDataBuilder }()
	userdataMock := &mockUserDataBuilder{
		userdata: mockedUserData,
	}
	newUserDataBuilder = func(clusterName string, tags []*ecs.Tag) userdata.UserDataBuilder {
		userdataMock.tags = tags
		return userdataMock
	}

	expectedCFNTags := []*sdkCFN.Tag{
		&sdkCFN.Tag{
			Key:   aws.String("madman"),
			Value: aws.String("with-a-box"),
		},
		&sdkCFN.Tag{
			Key:   aws.String("doctor"),
			Value: aws.String("11"),
		},
	}

	expectedECSTags := []*ecs.Tag{
		&ecs.Tag{
			Key:   aws.String("madman"),
			Value: aws.String("with-a-box"),
		},
		&ecs.Tag{
			Key:   aws.String("doctor"),
			Value: aws.String("11"),
		},
	}

	listSettingsResponse := &ecs.ListAccountSettingsOutput{
		Settings: []*ecs.Setting{
			&ecs.Setting{
				Name:  aws.String(ecs.SettingNameContainerInstanceLongArnFormat),
				Value: aws.String("enabled"),
			},
		},
	}

	gomock.InOrder(
		mockECS.EXPECT().ListAccountSettings(gomock.Any()).Return(listSettingsResponse, nil),
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil).Do(func(x, y interface{}) {
			actualTags := y.([]*ecs.Tag)
			assert.ElementsMatch(t, expectedECSTags, actualTags, "Expected tags to match")
		}),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("t2.micro").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Do(func(v, w, x, y, z interface{}) {
			actualTags := z.([]*sdkCFN.Tag)
			assert.ElementsMatch(t, expectedCFNTags, actualTags, "Expected tags to match")

			cfnParams := y.(*cloudformation.CfnStackParams)
			param, err := cfnParams.GetParameter(ParameterKeyUserData)
			assert.NoError(t, err, "Expected User Data parameter to be set")
			assert.Equal(t, mockedUserData, aws.StringValue(param.ParameterValue), "Expected user data to match")
		}).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
		mockCloudformation.EXPECT().DescribeNetworkResources(stackName).Return(nil),
	)
	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)
	globalSet := flag.NewFlagSet("ecs-cli", 0)
	globalContext := cli.NewContext(nil, globalSet, nil)

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.String(flags.ResourceTagsFlag, "madman=with-a-box,doctor=11", "")
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")

	context := cli.NewContext(nil, flagSet, globalContext)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = createCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error bringing up cluster")

	assert.Equal(t, userdataMock.tags, expectedECSTags, "Expected tags to match")
}

// /////////////////
// Cluster Down //
// ////////////////
func TestClusterDown(t *testing.T) {
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}
	defer os.Clearenv()

	gomock.InOrder(
		mockECS.EXPECT().IsActiveCluster(gomock.Any()).Return(true, nil),
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(nil),
		mockCloudformation.EXPECT().DeleteStack(stackName).Return(nil),
		mockCloudformation.EXPECT().WaitUntilDeleteComplete(stackName).Return(nil),
		mockECS.EXPECT().DeleteCluster(clusterName).Return(clusterName, nil),
	)
	flagSet := flag.NewFlagSet("ecs-cli-down", 0)
	flagSet.Bool(flags.ForceFlag, true, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = deleteCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error deleting cluster")
}

func TestClusterDownWithoutForce(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	flagSet := flag.NewFlagSet("ecs-cli-down", 0)
	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = deleteCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error when force deleting cluster")
}

func TestClusterDownForEmptyCluster(t *testing.T) {
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}
	defer os.Clearenv()

	gomock.InOrder(
		mockECS.EXPECT().IsActiveCluster(gomock.Any()).Return(true, nil),
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockECS.EXPECT().DeleteCluster(clusterName).Return(clusterName, nil),
	)

	flagSet := flag.NewFlagSet("ecs-cli-down", 0)
	flagSet.Bool(flags.ForceFlag, true, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = deleteCluster(context, awsClients, commandConfig)

	assert.NoError(t, err, "Unexpected error deleting cluster")
}

func TestDeleteClusterPrompt(t *testing.T) {
	readBuffer := bytes.NewBuffer([]byte("yes\ny\nno\n"))
	reader := bufio.NewReader(readBuffer)
	err := deleteClusterPrompt(reader)
	assert.NoError(t, err, "Expected no error with prompt to delete cluster")
	err = deleteClusterPrompt(reader)
	assert.NoError(t, err, "Expected no error with prompt to delete cluster")
	err = deleteClusterPrompt(reader)
	assert.Error(t, err, "Expected error with prompt to delete cluster")
}

///////////////////
// Cluster Scale //
//////////////////

func TestClusterScale(t *testing.T) {
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}
	defer os.Clearenv()

	mockECS.EXPECT().IsActiveCluster(gomock.Any()).Return(true, nil)

	existingParameters := []*sdkCFN.Parameter{
		&sdkCFN.Parameter{
			ParameterKey: aws.String("SomeParam1"),
		},
		&sdkCFN.Parameter{
			ParameterKey: aws.String("SomeParam2"),
		},
	}

	mockCloudformation.EXPECT().GetStackParameters(stackName).Return(existingParameters, nil)
	mockCloudformation.EXPECT().UpdateStack(gomock.Any(), gomock.Any()).Do(func(x, y interface{}) {
		observedStackName := x.(string)
		cfnParams := y.(*cloudformation.CfnStackParams)
		assert.Equal(t, stackName, observedStackName)
		_, err := cfnParams.GetParameter("SomeParam1")
		assert.NoError(t, err, "Unexpected error on scale.")
		_, err = cfnParams.GetParameter("SomeParam2")
		assert.NoError(t, err, "Unexpected error on scale.")
		param, err := cfnParams.GetParameter(ParameterKeyAsgMaxSize)
		assert.NoError(t, err, "Unexpected error on scale.")
		assert.Equal(t, "1", aws.StringValue(param.ParameterValue))
	}).Return("", nil)
	mockCloudformation.EXPECT().WaitUntilUpdateComplete(stackName).Return(nil)

	flagSet := flag.NewFlagSet("ecs-cli-down", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")
	flagSet.String(flags.AsgMaxSizeFlag, "1", "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = scaleCluster(context, awsClients, commandConfig)
	assert.NoError(t, err, "Unexpected error scaling cluster")
}

func TestClusterScaleWithoutIamCapability(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.String(flags.AsgMaxSizeFlag, "1", "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = scaleCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error scaling cluster when iam capability is not specified")
}

func TestClusterScaleWithoutSize(t *testing.T) {
	defer os.Clearenv()
	mockECS, mockCloudformation, mockSSM, mockEC2 := setupTest(t)
	awsClients := &AWSClients{mockECS, mockCloudformation, mockSSM, mockEC2}

	flagSet := flag.NewFlagSet("ecs-cli-up", 0)
	flagSet.Bool(flags.CapabilityIAMFlag, true, "")

	context := cli.NewContext(nil, flagSet, nil)
	rdwr := newMockReadWriter()
	commandConfig, err := newCommandConfig(context, rdwr)
	assert.NoError(t, err, "Unexpected error creating CommandConfig")

	err = scaleCluster(context, awsClients, commandConfig)
	assert.Error(t, err, "Expected error scaling cluster when size is not specified")
}

/////////////////
// Cluster PS //
////////////////

func TestClusterPSTaskGetInfoFail(t *testing.T) {
	testSession, err := session.NewSession()
	assert.NoError(t, err, "Unexpected error in creating session")

	newCommandConfig = func(context *cli.Context, rdwr config.ReadWriter) (*config.CommandConfig, error) {
		return &config.CommandConfig{
			Cluster: clusterName,
			Session: testSession,
		}, nil
	}
	defer os.Clearenv()
	mockECS, _, _, _ := setupTest(t)

	mockECS.EXPECT().IsActiveCluster(gomock.Any()).Return(true, nil)
	mockECS.EXPECT().GetTasksPages(gomock.Any(), gomock.Any()).Do(func(x, y interface{}) {
	}).Return(errors.New("error"))

	flagSet := flag.NewFlagSet("ecs-cli-down", 0)

	context := cli.NewContext(nil, flagSet, nil)
	_, err = clusterPS(context, newMockReadWriter())
	assert.Error(t, err, "Expected error in cluster ps")
}

/////////////////////
// private methods //
/////////////////////

func amiMetadata(imageID string) *amimetadata.AMIMetadata {
	return &amimetadata.AMIMetadata{
		ImageID:        imageID,
		OsName:         "Amazon Linux",
		AgentVersion:   "1.7.2",
		RuntimeVersion: "Docker version 17.12.1-ce",
	}
}

func mocksForSuccessfulClusterUp(mockECS *mock_ecs.MockECSClient, mockCloudformation *mock_cloudformation.MockCloudformationClient, mockSSM *mock_amimetadata.MockClient, mockEC2 *mock_ec2.MockEC2Client) {
	gomock.InOrder(
		mockECS.EXPECT().CreateCluster(clusterName, gomock.Any()).Return(clusterName, nil),
	)
	gomock.InOrder(
		mockSSM.EXPECT().GetRecommendedECSLinuxAMI("t2.micro").Return(amiMetadata(amiID), nil),
	)
	gomock.InOrder(
		mockCloudformation.EXPECT().ValidateStackExists(stackName).Return(errors.New("error")),
		mockCloudformation.EXPECT().CreateStack(gomock.Any(), stackName, true, gomock.Any(), gomock.Any()).Return("", nil),
		mockCloudformation.EXPECT().WaitUntilCreateComplete(stackName).Return(nil),
	)
	gomock.InOrder(
		mockEC2.EXPECT().DescribeInstanceTypeOfferings("us-west-1").Return([]string{"t2.micro"}, nil),
	)
}
