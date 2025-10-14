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
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"sigs.k8s.io/cluster-api-provider-aws/v2/pkg/cloud/awserrors"
	"sigs.k8s.io/cluster-api-provider-aws/v2/pkg/record"
)

// Package-level cache for instance type capacity information.
// This cache is shared across all EC2 service instances to reduce AWS API calls.
var instanceTypeCapacityCache sync.Map

// GetInstanceTypeCapacity returns the capacity (CPU, memory, GPU) for a given instance type.
// It caches results to reduce AWS API calls. This is used to populate the capacity field
// in machine templates and machine pools for autoscaling from zero.
// See: https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20210310-opt-in-autoscaling-from-zero.md
func (s *Service) GetInstanceTypeCapacity(instanceType ec2types.InstanceType) (corev1.ResourceList, error) {
	// 1. Check cache first
	if cached, ok := instanceTypeCapacityCache.Load(string(instanceType)); ok {
		return cached.(corev1.ResourceList), nil
	}

	// 2. Call AWS DescribeInstanceTypes API
	input := &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{instanceType},
	}

	result, err := s.EC2Client.DescribeInstanceTypes(context.TODO(), input)
	if err != nil {
		// Handle permission errors gracefully - capacity population is opportunistic
		if awserrors.IsPermissionsError(err) {
			record.Warnf(s.scope.InfraCluster(), "FailedDescribeInstanceTypes",
				"insufficient permissions to describe instance types for instance type %q, capacity will not be populated: %v",
				instanceType, err)
			return corev1.ResourceList{}, nil
		}
		return corev1.ResourceList{}, errors.Wrapf(err, "failed to describe instance types for instance type %q", instanceType)
	}

	// 3. Validate response
	if len(result.InstanceTypes) == 0 {
		s.scope.GetLogger().Error(fmt.Errorf("empty response"), "instance type not found", "instanceType", instanceType)
		return corev1.ResourceList{}, nil
	}

	// 4. Convert to Kubernetes ResourceList
	capacity := s.convertToResourceList(result.InstanceTypes[0])

	// 5. Store in cache
	instanceTypeCapacityCache.Store(string(instanceType), capacity)

	return capacity, nil
}

// convertToResourceList converts AWS instance type info to Kubernetes ResourceList.
func (s *Service) convertToResourceList(instanceTypeInfo ec2types.InstanceTypeInfo) corev1.ResourceList {
	capacity := corev1.ResourceList{}

	// CPU: VCpus → millicores (1 vCPU = 1000 millicores)
	if instanceTypeInfo.VCpuInfo != nil && instanceTypeInfo.VCpuInfo.DefaultVCpus != nil {
		cpuMillicores := int64(*instanceTypeInfo.VCpuInfo.DefaultVCpus) * 1000
		capacity[corev1.ResourceCPU] = *resource.NewMilliQuantity(cpuMillicores, resource.DecimalSI)
	}

	// Memory: MiB → bytes (1 MiB = 1024 * 1024 bytes)
	if instanceTypeInfo.MemoryInfo != nil && instanceTypeInfo.MemoryInfo.SizeInMiB != nil {
		memoryBytes := *instanceTypeInfo.MemoryInfo.SizeInMiB * 1024 * 1024
		capacity[corev1.ResourceMemory] = *resource.NewQuantity(memoryBytes, resource.BinarySI)
	}

	// GPU: Count (if present)
	if instanceTypeInfo.GpuInfo != nil && len(instanceTypeInfo.GpuInfo.Gpus) > 0 {
		gpuCount := int64(0)
		for _, gpu := range instanceTypeInfo.GpuInfo.Gpus {
			if gpu.Count != nil {
				gpuCount += int64(*gpu.Count)
			}
		}
		if gpuCount > 0 {
			capacity[corev1.ResourceName("nvidia.com/gpu")] = *resource.NewQuantity(gpuCount, resource.DecimalSI)
		}
	}

	return capacity
}
