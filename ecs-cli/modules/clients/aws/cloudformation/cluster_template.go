// Copyright 2015-2017 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//  http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package cloudformation

import (
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
)

func GetClusterTemplate(tags []*ecs.Tag, stackName string) (string, error) {
	tagJSON, err := json.Marshal(tags)
	if err != nil {
		return "", err
	}

	asgTags := getASGTags(tags, stackName)
	asgTagJSON, err := json.Marshal(asgTags)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(clusterTemplate, string(tagJSON), string(asgTagJSON)), nil
}

// Autoscaling CFN tags have an additional field that determines if they are
// propagated to the EC2 instances launched
// ECS CLI also adds a 'Name' tag
// (unless customer specifies a Name; only one name is allowed by the API)
func getASGTags(tags []*ecs.Tag, stackName string) []autoscalingTag {
	asgTags := []autoscalingTag{}
	addName := true
	for _, tag := range tags {
		asgTag := autoscalingTag{
			Key:               aws.StringValue(tag.Key),
			Value:             aws.StringValue(tag.Value),
			PropagateAtLaunch: true,
		}
		if asgTag.Key == "Name" {
			addName = false
		}
		asgTags = append(asgTags, asgTag)
	}

	if addName {
		asgTags = append(asgTags, autoscalingTag{
			Key:               "Name",
			Value:             fmt.Sprintf("ECS Instance - %s", stackName),
			PropagateAtLaunch: true,
		})
	}

	return asgTags
}

// custom struct needed because sdk's autoscaling.Tag contains additional
// fields that aren't valid in CFN
type autoscalingTag struct {
	Key               string
	Value             string
	PropagateAtLaunch bool
}

// TODO: Improvements:
// 1. Auto detect default vpc
// 2. Auto detect existing key pairs
// 3. Create key pair when none exist
// 4. Remove the hardcoded 2 subnets creation

// These are used to display CFN resources in the CreateCluster callback.
// TODO: Find better way to use constants in template string itself.
const (
	Subnet1LogicalResourceId       = "PubSubnetAz1"
	Subnet2LogicalResourceId       = "PubSubnetAz2"
	VPCLogicalResourceId           = "Vpc"
	SecurityGroupLogicalResourceId = "EcsSecurityGroup"
	DefaultECSInstanceType         = "t2.micro"
)

var clusterTemplate = `
{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "AWS CloudFormation template to create resources required to run tasks on an ECS cluster.",
  "Mappings": {
    "VpcCidrs": {
      "vpc": {"cidr" : "10.0.0.0/16"},
      "pubsubnet1": {"cidr" : "10.0.0.0/24"},
      "pubsubnet2": {"cidr" :"10.0.1.0/24"}
    }
  },
  "Parameters": {
    "EcsAmiId": {
      "Type": "String",
      "Description": "ECS EC2 AMI id",
      "Default": ""
    },
    "EcsInstanceType": {
      "Type": "String",
      "Description": "ECS EC2 instance type",
      "Default": ""
    },
    "SpotPrice": {
      "Type": "Number",
      "Description": "If greater than 0, then a EC2 Spot instance will be requested",
      "Default": "0"
    },
    "KeyName": {
      "Type": "String",
      "Description": "Optional - Name of an existing EC2 KeyPair to enable SSH access to the ECS instances",
      "Default": ""
    },
    "VpcId": {
      "Type": "String",
      "Description": "Optional - VPC Id of existing VPC. Leave blank to have a new VPC created",
      "Default": "",
      "AllowedPattern": "^(?:vpc-[0-9a-f]{8}|vpc-[0-9a-f]{17}|)$",
      "ConstraintDescription": "VPC Id must begin with 'vpc-' followed by either an 8 or 17 character identifier, or leave blank to have a new VPC created"
    },
    "SubnetIds": {
      "Type": "CommaDelimitedList",
      "Description": "Optional - Comma separated list of two (2) existing VPC Subnet Ids where ECS instances will run.  Required if setting VpcId.",
      "Default": ""
    },
    "AsgMaxSize": {
      "Type": "Number",
      "Description": "Maximum size and initial Desired Capacity of ECS Auto Scaling Group",
      "Default": "1"
    },
    "SecurityGroupIds": {
      "Type": "CommaDelimitedList",
      "Description": "Optional - Existing security group to associate the container instances. Creates one by default.",
      "Default": ""
    },
    "SourceCidr": {
      "Type": "String",
      "Description": "Optional - CIDR/IP range for EcsPort - defaults to 0.0.0.0/0",
      "Default": "0.0.0.0/0"
    },
    "EcsPort" : {
      "Type" : "String",
      "Description" : "Optional - Security Group port to open on ECS instances - defaults to port 80",
      "Default" : "80"
    },
    "VpcAvailabilityZones": {
      "Type": "CommaDelimitedList",
      "Description": "Optional - Comma-delimited list of VPC availability zones in which to create subnets.  Required if setting VpcId.",
      "Default": ""
    },
    "AssociatePublicIpAddress": {
      "Type": "String",
      "Description": "Optional - Automatically assign public IP addresses to new instances in this VPC.",
      "Default": "true"
    },
    "EcsCluster" : {
      "Type" : "String",
      "Description" : "ECS Cluster Name",
      "Default" : "default"
    },
    "InstanceRole" : {
      "Type" : "String",
      "Description" : "Optional - Instance IAM Role.",
      "Default" : ""
    },
    "IsFargate": {
      "Type": "String",
      "Description": "Optional - Whether to create resources only for running Fargate tasks.",
      "Default": "false"
    },
    "IsIMDSv2": {
      "Type": "String",
      "Description": "Optional - Disable IMDSv1.",
      "Default": "false",
    },
    "UserData" : {
      "Type" : "String",
      "Description" : "User data for EC2 instances. Required for EC2 launch type, ignored with Fargate",
      "Default" : ""
    }
  },
  "Conditions": {
    "IsCNRegion": {
      "Fn::Or" : [
        {"Fn::Equals": [ { "Ref": "AWS::Region" }, "cn-north-1" ]},
        {"Fn::Equals": [ { "Ref": "AWS::Region" }, "cn-northwest-1" ]}
      ]
    },
    "LaunchInstances": {
      "Fn::Equals": [ { "Ref": "IsFargate" }, "false" ]
    },
    "EnableIMDSv2": {
      "Fn::Equals": [ { "Ref": "IsIMDSv2" }, "true" ]
    },
    "CreateVpcResources": {
      "Fn::Equals": [
        {
          "Ref": "VpcId"
        },
        ""
      ]
    },
    "CreateSecurityGroup": {
      "Fn::And":[
        {
          "Condition": "LaunchInstances"
        },
        {
          "Fn::Equals": [
            {
              "Fn::Join": [
                "",
                {
                  "Ref": "SecurityGroupIds"
                }
              ]
            },
            ""
          ]
        }
      ]
    },
    "CreateEC2LCWithKeyPair": {
      "Fn::And":[
        {
          "Condition": "LaunchInstances"
        },
        {
          "Fn::Not": [
            {
              "Fn::Equals": [
                {
                  "Ref": "KeyName"
                },
                ""
              ]
            }
          ]
        }
      ]
    },
    "UseSpecifiedVpcAvailabilityZones": {
      "Fn::Not": [
        {
          "Fn::Equals": [
            {
              "Fn::Join": [
                "",
                {
                  "Ref": "VpcAvailabilityZones"
                }
              ]
            },
            ""
          ]
        }
      ]
    },
    "CreateEcsInstanceRole": {
      "Fn::And":[
        {
          "Condition": "LaunchInstances"
        },
        {
          "Fn::Equals": [
            {
              "Ref": "InstanceRole"
            },
            ""
          ]
        }
      ]
    },
    "UseSpotInstances": {
      "Fn::Not": [
      {
        "Fn::Equals": [
        {
          "Ref": "SpotPrice"
        },
        0
        ]
      }
      ]
    }
  },
  "Resources": {
    "Vpc": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::VPC",
      "Properties": {
        "EnableDnsSupport" : true,
        "EnableDnsHostnames" : true,
        "CidrBlock": {
          "Fn::FindInMap": ["VpcCidrs", "vpc", "cidr"]
        },
        "Tags": %[1]s
      }
    },
    "PubSubnetAz1": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::Subnet",
      "Properties": {
        "VpcId": {
          "Ref": "Vpc"
        },
        "CidrBlock": {
          "Fn::FindInMap": ["VpcCidrs", "pubsubnet1", "cidr"]
        },
        "Tags": %[1]s,
        "AvailabilityZone": {
          "Fn::If": [
            "UseSpecifiedVpcAvailabilityZones",
            {
              "Fn::Select": [
                "0",
                {
                  "Ref": "VpcAvailabilityZones"
                }
              ]
            },
            {
              "Fn::Select": [
                "0",
                {
                  "Fn::GetAZs": {
                    "Ref": "AWS::Region"
                  }
                }
              ]
            }
          ]
        }
      }
    },
    "PubSubnetAz2": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::Subnet",
      "Properties": {
        "VpcId": {
          "Ref": "Vpc"
        },
        "CidrBlock": {
          "Fn::FindInMap": ["VpcCidrs", "pubsubnet2", "cidr"]
        },
        "Tags": %[1]s,
        "AvailabilityZone": {
          "Fn::If": [
            "UseSpecifiedVpcAvailabilityZones",
            {
              "Fn::Select": [
                "1",
                {
                  "Ref": "VpcAvailabilityZones"
                }
              ]
            },
            {
              "Fn::Select": [
                "1",
                {
                  "Fn::GetAZs": {
                    "Ref": "AWS::Region"
                  }
                }
              ]
            }
          ]
        }
      }
    },
    "InternetGateway": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::InternetGateway",
      "Properties": {
        "Tags": %[1]s
      }
    },
    "AttachGateway": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::VPCGatewayAttachment",
      "Properties": {
        "VpcId": {
          "Ref": "Vpc"
        },
        "InternetGatewayId": {
          "Ref": "InternetGateway"
        }
      }
    },
    "RouteViaIgw": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::RouteTable",
      "Properties": {
        "VpcId": {
          "Ref": "Vpc"
        },
        "Tags": %[1]s
      }
    },
    "PublicRouteViaIgw": {
      "Condition": "CreateVpcResources",
      "DependsOn": "AttachGateway",
      "Type": "AWS::EC2::Route",
      "Properties": {
        "RouteTableId": {
          "Ref": "RouteViaIgw"
        },
        "DestinationCidrBlock": "0.0.0.0/0",
        "GatewayId": {
          "Ref": "InternetGateway"
        }
      }
    },
    "PubSubnet1RouteTableAssociation": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::SubnetRouteTableAssociation",
      "Properties": {
        "SubnetId": {
          "Ref": "PubSubnetAz1"
        },
        "RouteTableId": {
          "Ref": "RouteViaIgw"
        }
      }
    },
    "PubSubnet2RouteTableAssociation": {
      "Condition": "CreateVpcResources",
      "Type": "AWS::EC2::SubnetRouteTableAssociation",
      "Properties": {
        "SubnetId": {
          "Ref": "PubSubnetAz2"
        },
        "RouteTableId": {
          "Ref": "RouteViaIgw"
        }
      }
    },
    "EcsSecurityGroup": {
      "Condition": "CreateSecurityGroup",
      "Type": "AWS::EC2::SecurityGroup",
      "Properties": {
        "GroupDescription": "ECS Allowed Ports",
        "Tags": %[1]s,
        "VpcId": {
          "Fn::If": [
            "CreateVpcResources",
            {
              "Ref": "Vpc"
            },
            {
              "Ref": "VpcId"
            }
          ]
        },
        "SecurityGroupIngress" : [ {
            "IpProtocol" : "tcp",
            "FromPort" : { "Ref" : "EcsPort" },
            "ToPort" : { "Ref" : "EcsPort" },
            "CidrIp" : { "Ref" : "SourceCidr" }
        } ]
      }
    },
    "EcsInstanceRole": {
      "Condition": "CreateEcsInstanceRole",
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Principal": {
                "Service": [
                  "Fn::If": [
                    "IsCNRegion",
                    "ec2.amazonaws.com.cn",
                    "ec2.amazonaws.com"
                  ]
                ]
              },
              "Action": [
                "sts:AssumeRole"
              ]
            }
          ]
        },
        "Path": "/",
        "ManagedPolicyArns": [
          "arn:aws:iam::aws:policy/service-role/AmazonEC2ContainerServiceforEC2Role"
        ]
      }
    },
    "EcsInstanceProfile": {
      "Condition": "LaunchInstances",
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "Path": "/",
        "Roles": [
          "Fn::If": [
            "CreateEcsInstanceRole",
            {
              "Ref": "EcsInstanceRole"
            },
            {
              "Ref": "InstanceRole"
            }
          ]
        ]
      }
    },
    "EcsInstanceLc": {
      "Condition": "LaunchInstances",
      "Type": "AWS::AutoScaling::LaunchConfiguration",
      "Properties": {
        "ImageId": { "Ref" : "EcsAmiId" },
        "InstanceType": {
          "Ref": "EcsInstanceType"
        },
        "SpotPrice": {
          "Fn::If": [
            "UseSpotInstances",
            {
              "Ref": "SpotPrice"
            },
            {
              "Ref": "AWS::NoValue"
            }
          ]
        },
        "AssociatePublicIpAddress": {
          "Ref": "AssociatePublicIpAddress"
        },
        "IamInstanceProfile": {
          "Ref": "EcsInstanceProfile"
        },
        "KeyName": {
          "Fn::If": [
            "CreateEC2LCWithKeyPair",
            {
              "Ref": "KeyName"
            },
            {
              "Ref": "AWS::NoValue"
            }
          ]
        },
        "MetadataOptions": {
          "Fn::If": [
            "EnableIMDSv2",
            {
              "HttpEndpoint": "enabled",
              "HttpTokens": "required"
            },
            {
              "Ref": "AWS::NoValue"
            }
          ]
        },
        "SecurityGroups": {
          "Fn::If": [
            "CreateSecurityGroup",
            [ {
              "Ref": "EcsSecurityGroup"
            } ],
            {
              "Ref": "SecurityGroupIds"
            }
          ]
        },
        "UserData": {
          "Fn::Base64": {
            "Ref": "UserData"
          }
        }
      }
    },
    "EcsInstanceAsg": {
      "Condition": "LaunchInstances",
      "Type": "AWS::AutoScaling::AutoScalingGroup",
      "Properties": {
        "VPCZoneIdentifier": {
          "Fn::If": [
            "CreateVpcResources",
            [
              {
                "Fn::Join": [
                  ",",
                  [
                    {
                      "Ref": "PubSubnetAz1"
                    },
                    {
                      "Ref": "PubSubnetAz2"
                    }
                  ]
                ]
              }
            ],
            {
              "Ref": "SubnetIds"
            }
          ]
        },
        "LaunchConfigurationName": {
          "Ref": "EcsInstanceLc"
        },
        "MinSize": "0",
        "MaxSize": {
          "Ref": "AsgMaxSize"
        },
        "DesiredCapacity": {
          "Ref": "AsgMaxSize"
        },
        "Tags": %[2]s
      }
    }
  }
}
`
