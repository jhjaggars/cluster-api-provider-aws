/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ec2

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/cluster-api-provider-aws/v2/pkg/cloud/awserrors"
	"sigs.k8s.io/cluster-api-provider-aws/v2/test/mocks"
)

func TestGetInstanceTypeCapacity(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	testCases := []struct {
		name           string
		instanceType   ec2types.InstanceType
		expect         func(m *mocks.MockEC2APIMockRecorder)
		checkCapacity  func(g *WithT, capacity corev1.ResourceList)
		checkError     func(g *WithT, err error)
		clearCache     bool
	}{
		{
			name:         "t3.medium - success with CPU and memory",
			instanceType: "t3.medium",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(&ec2.DescribeInstanceTypesOutput{
						InstanceTypes: []ec2types.InstanceTypeInfo{
							{
								InstanceType: "t3.medium",
								VCpuInfo: &ec2types.VCpuInfo{
									DefaultVCpus: aws.Int32(2),
								},
								MemoryInfo: &ec2types.MemoryInfo{
									SizeInMiB: aws.Int64(4096),
								},
							},
						},
					}, nil)
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(HaveLen(2))
				expectedCPU := resource.MustParse("2000m")
				expectedMemory := resource.MustParse("4Gi")
				cpu := capacity[corev1.ResourceCPU]
				memory := capacity[corev1.ResourceMemory]
				g.Expect(cpu.Cmp(expectedCPU)).To(Equal(0))
				g.Expect(memory.Cmp(expectedMemory)).To(Equal(0))
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name:         "p3.2xlarge - GPU instance",
			instanceType: "p3.2xlarge",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(&ec2.DescribeInstanceTypesOutput{
						InstanceTypes: []ec2types.InstanceTypeInfo{
							{
								InstanceType: "p3.2xlarge",
								VCpuInfo: &ec2types.VCpuInfo{
									DefaultVCpus: aws.Int32(8),
								},
								MemoryInfo: &ec2types.MemoryInfo{
									SizeInMiB: aws.Int64(61440),
								},
								GpuInfo: &ec2types.GpuInfo{
									Gpus: []ec2types.GpuDeviceInfo{
										{
											Name:         aws.String("V100"),
											Manufacturer: aws.String("NVIDIA"),
											Count:        aws.Int32(1),
										},
									},
								},
							},
						},
					}, nil)
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(HaveLen(3))
				cpu := capacity[corev1.ResourceCPU]
				memory := capacity[corev1.ResourceMemory]
				gpu := capacity[corev1.ResourceName("nvidia.com/gpu")]
				g.Expect(cpu.Cmp(resource.MustParse("8000m"))).To(Equal(0))
				g.Expect(memory.Cmp(resource.MustParse("60Gi"))).To(Equal(0))
				g.Expect(gpu.Cmp(resource.MustParse("1"))).To(Equal(0))
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name:         "p3.8xlarge - multiple GPUs",
			instanceType: "p3.8xlarge",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(&ec2.DescribeInstanceTypesOutput{
						InstanceTypes: []ec2types.InstanceTypeInfo{
							{
								InstanceType: "p3.8xlarge",
								VCpuInfo: &ec2types.VCpuInfo{
									DefaultVCpus: aws.Int32(32),
								},
								MemoryInfo: &ec2types.MemoryInfo{
									SizeInMiB: aws.Int64(244736),
								},
								GpuInfo: &ec2types.GpuInfo{
									Gpus: []ec2types.GpuDeviceInfo{
										{
											Name:         aws.String("V100"),
											Manufacturer: aws.String("NVIDIA"),
											Count:        aws.Int32(4),
										},
									},
								},
							},
						},
					}, nil)
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(HaveLen(3))
				cpu := capacity[corev1.ResourceCPU]
				gpu := capacity[corev1.ResourceName("nvidia.com/gpu")]
				g.Expect(cpu.Cmp(resource.MustParse("32000m"))).To(Equal(0))
				g.Expect(gpu.Cmp(resource.MustParse("4"))).To(Equal(0))
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name:         "permission error - graceful degradation",
			instanceType: "t3.large",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(nil, &smithy.GenericAPIError{
						Code:    awserrors.UnauthorizedOperation,
						Message: "You are not authorized to perform this operation",
					})
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(BeEmpty())
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name:         "empty response",
			instanceType: "invalid.type",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(&ec2.DescribeInstanceTypesOutput{
						InstanceTypes: []ec2types.InstanceTypeInfo{},
					}, nil)
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(BeEmpty())
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name:         "nil VCpuInfo - safe handling",
			instanceType: "custom.type",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(&ec2.DescribeInstanceTypesOutput{
						InstanceTypes: []ec2types.InstanceTypeInfo{
							{
								InstanceType: "custom.type",
								VCpuInfo:     nil,
								MemoryInfo: &ec2types.MemoryInfo{
									SizeInMiB: aws.Int64(4096),
								},
							},
						},
					}, nil)
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(HaveLen(1))
				memory := capacity[corev1.ResourceMemory]
				g.Expect(memory.Cmp(resource.MustParse("4Gi"))).To(Equal(0))
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name:         "nil MemoryInfo - safe handling",
			instanceType: "custom.type2",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(&ec2.DescribeInstanceTypesOutput{
						InstanceTypes: []ec2types.InstanceTypeInfo{
							{
								InstanceType: "custom.type2",
								VCpuInfo: &ec2types.VCpuInfo{
									DefaultVCpus: aws.Int32(2),
								},
								MemoryInfo: nil,
							},
						},
					}, nil)
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(HaveLen(1))
				cpu := capacity[corev1.ResourceCPU]
				g.Expect(cpu.Cmp(resource.MustParse("2000m"))).To(Equal(0))
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).NotTo(HaveOccurred())
			},
		},
		{
			name:         "API error - non-permission error",
			instanceType: "t3.nano",
			clearCache:   true,
			expect: func(m *mocks.MockEC2APIMockRecorder) {
				m.DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
					Return(nil, awserrors.NewFailedDependency("service unavailable"))
			},
			checkCapacity: func(g *WithT, capacity corev1.ResourceList) {
				g.Expect(capacity).To(BeEmpty())
			},
			checkError: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Clear cache before test if requested
			if tc.clearCache {
				instanceTypeCapacityCache.Delete(string(tc.instanceType))
			}

			scheme, err := setupScheme()
			g.Expect(err).NotTo(HaveOccurred())
			client := fake.NewClientBuilder().WithScheme(scheme).Build()

			clusterScope, err := setupClusterScope(client)
			g.Expect(err).NotTo(HaveOccurred())

			ec2Mock := mocks.NewMockEC2API(mockCtrl)
			tc.expect(ec2Mock.EXPECT())

			svc := &Service{
				scope:     clusterScope,
				EC2Client: ec2Mock,
			}

			capacity, err := svc.GetInstanceTypeCapacity(tc.instanceType)

			tc.checkCapacity(g, capacity)
			tc.checkError(g, err)
		})
	}
}

func TestGetInstanceTypeCapacityCaching(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	g := NewWithT(t)

	instanceType := ec2types.InstanceType("t3.xlarge")

	// Clear cache before test
	instanceTypeCapacityCache.Delete(string(instanceType))

	scheme, err := setupScheme()
	g.Expect(err).NotTo(HaveOccurred())
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	clusterScope, err := setupClusterScope(client)
	g.Expect(err).NotTo(HaveOccurred())

	ec2Mock := mocks.NewMockEC2API(mockCtrl)

	// First call should hit the AWS API
	ec2Mock.EXPECT().DescribeInstanceTypes(context.TODO(), gomock.AssignableToTypeOf(&ec2.DescribeInstanceTypesInput{})).
		Return(&ec2.DescribeInstanceTypesOutput{
			InstanceTypes: []ec2types.InstanceTypeInfo{
				{
					InstanceType: instanceType,
					VCpuInfo: &ec2types.VCpuInfo{
						DefaultVCpus: aws.Int32(4),
					},
					MemoryInfo: &ec2types.MemoryInfo{
						SizeInMiB: aws.Int64(16384),
					},
				},
			},
		}, nil).Times(1) // Should only be called once

	svc := &Service{
		scope:     clusterScope,
		EC2Client: ec2Mock,
	}

	// First call - should hit AWS API
	capacity1, err := svc.GetInstanceTypeCapacity(instanceType)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(capacity1).To(HaveLen(2))
	cpu1 := capacity1[corev1.ResourceCPU]
	memory1 := capacity1[corev1.ResourceMemory]
	g.Expect(cpu1.Cmp(resource.MustParse("4000m"))).To(Equal(0))
	g.Expect(memory1.Cmp(resource.MustParse("16Gi"))).To(Equal(0))

	// Second call - should use cache (no additional AWS API call)
	capacity2, err := svc.GetInstanceTypeCapacity(instanceType)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(capacity2).To(Equal(capacity1))

	// Third call - should also use cache
	capacity3, err := svc.GetInstanceTypeCapacity(instanceType)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(capacity3).To(Equal(capacity1))
}

func TestConvertToResourceList(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	g := NewWithT(t)

	scheme, err := setupScheme()
	g.Expect(err).NotTo(HaveOccurred())
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	clusterScope, err := setupClusterScope(client)
	g.Expect(err).NotTo(HaveOccurred())

	svc := &Service{
		scope: clusterScope,
	}

	testCases := []struct {
		name          string
		instanceInfo  ec2types.InstanceTypeInfo
		expectedLen   int
		expectedCPU   string
		expectedMemory string
		expectedGPU   string
	}{
		{
			name: "standard instance",
			instanceInfo: ec2types.InstanceTypeInfo{
				VCpuInfo: &ec2types.VCpuInfo{
					DefaultVCpus: aws.Int32(2),
				},
				MemoryInfo: &ec2types.MemoryInfo{
					SizeInMiB: aws.Int64(4096),
				},
			},
			expectedLen:    2,
			expectedCPU:    "2000m",
			expectedMemory: "4Gi",
		},
		{
			name: "GPU instance with single GPU",
			instanceInfo: ec2types.InstanceTypeInfo{
				VCpuInfo: &ec2types.VCpuInfo{
					DefaultVCpus: aws.Int32(8),
				},
				MemoryInfo: &ec2types.MemoryInfo{
					SizeInMiB: aws.Int64(61440),
				},
				GpuInfo: &ec2types.GpuInfo{
					Gpus: []ec2types.GpuDeviceInfo{
						{Count: aws.Int32(1)},
					},
				},
			},
			expectedLen:    3,
			expectedCPU:    "8000m",
			expectedMemory: "60Gi",
			expectedGPU:    "1",
		},
		{
			name: "empty instance info",
			instanceInfo: ec2types.InstanceTypeInfo{},
			expectedLen:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			capacity := svc.convertToResourceList(tc.instanceInfo)

			g.Expect(capacity).To(HaveLen(tc.expectedLen))

			if tc.expectedCPU != "" {
				cpu := capacity[corev1.ResourceCPU]
				g.Expect(cpu.Cmp(resource.MustParse(tc.expectedCPU))).To(Equal(0))
			}

			if tc.expectedMemory != "" {
				memory := capacity[corev1.ResourceMemory]
				g.Expect(memory.Cmp(resource.MustParse(tc.expectedMemory))).To(Equal(0))
			}

			if tc.expectedGPU != "" {
				gpu := capacity[corev1.ResourceName("nvidia.com/gpu")]
				g.Expect(gpu.Cmp(resource.MustParse(tc.expectedGPU))).To(Equal(0))
			}
		})
	}
}
