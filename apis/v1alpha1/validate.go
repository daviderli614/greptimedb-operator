// Copyright 2024 Greptime Team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	"context"
	"fmt"

	"github.com/pelletier/go-toml"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Validate checks the GreptimeDBCluster and returns an error if it is invalid.
func (in *GreptimeDBCluster) Validate() error {
	if in == nil {
		return nil
	}

	if err := in.validateFrontend(); err != nil {
		return err
	}

	if err := in.validateFrontendGroups(); err != nil {
		return err
	}

	if err := in.validateMeta(); err != nil {
		return err
	}

	if in.GetDatanode() != nil && len(in.GetDatanodeGroups()) > 0 {
		return fmt.Errorf("datanode and datanodeGroups cannot be set at the same time")
	}

	if err := in.validateDatanode(); err != nil {
		return err
	}

	if err := in.validateDatanodeGroups(); err != nil {
		return err
	}

	if in.GetFlownode() != nil {
		if err := in.validateFlownode(); err != nil {
			return err
		}
	}

	if wal := in.GetWALProvider(); wal != nil {
		if err := validateWALProvider(wal); err != nil {
			return err
		}
	}

	if osp := in.GetObjectStorageProvider(); osp != nil {
		if err := validateObjectStorageProvider(osp); err != nil {
			return err
		}
	}

	return nil
}

// Check checks the GreptimeDBCluster with other resources and returns an error if it is invalid.
func (in *GreptimeDBCluster) Check(ctx context.Context, client client.Client) error {
	// Check if the TLS secret exists and contains the required keys.
	if secretName := in.GetFrontend().GetTLS().GetSecretName(); secretName != "" {
		if err := checkTLSSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	// Check if the PodMonitor CRD exists.
	if in.GetPrometheusMonitor().IsEnablePrometheusMonitor() {
		if err := checkPodMonitorExists(ctx, client); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetS3Storage().GetSecretName(); secretName != "" {
		if err := checkS3CredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetOSSStorage().GetSecretName(); secretName != "" {
		if err := checkOSSCredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetGCSStorage().GetSecretName(); secretName != "" {
		if err := checkGCSCredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetAZBlobStorage().GetSecretName(); secretName != "" {
		if err := checkAZBlobCredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	if secretName := in.GetMeta().GetBackendStorage().GetMySQLStorage().GetCredentialsSecretName(); secretName != "" {
		if err := checkSecretData(ctx, client, in.GetNamespace(), secretName, []string{MetaDatabaseUsernameKey, MetaDatabasePasswordKey}); err != nil {
			return err
		}
	}

	if secretName := in.GetMeta().GetBackendStorage().GetPostgreSQLStorage().GetCredentialsSecretName(); secretName != "" {
		if err := checkSecretData(ctx, client, in.GetNamespace(), secretName, []string{MetaDatabaseUsernameKey, MetaDatabasePasswordKey}); err != nil {
			return err
		}
	}

	return nil
}

func (in *GreptimeDBCluster) validateFrontend() error {
	if err := validateTomlConfig(in.GetFrontend().GetConfig()); err != nil {
		return fmt.Errorf("invalid frontend toml config: '%v'", err)
	}

	return nil
}

func (in *GreptimeDBCluster) validateFrontendGroups() error {
	for _, frontend := range in.GetFrontendGroups() {
		if len(frontend.GetName()) == 0 {
			return fmt.Errorf("the frontend name must be specified")
		}

		if err := validateTomlConfig(frontend.GetConfig()); err != nil {
			return fmt.Errorf("invalid frontend toml config: '%v'", err)
		}
	}

	return nil
}

func (in *GreptimeDBCluster) validateDatanodeGroups() error {
	for _, datanode := range in.GetDatanodeGroups() {
		if len(datanode.GetName()) == 0 {
			return fmt.Errorf("the datanode group name must be specified")
		}

		if err := validateTomlConfig(datanode.GetConfig()); err != nil {
			return fmt.Errorf("invalid datanode toml config: '%v'", err)
		}
	}

	return nil
}

func (in *GreptimeDBCluster) validateMeta() error {
	if err := validateTomlConfig(in.GetMeta().GetConfig()); err != nil {
		return fmt.Errorf("invalid meta toml config: '%v'", err)
	}

	return nil
}

func (in *GreptimeDBCluster) validateMetaBackendStorage() error {
	backendStorage := in.GetMeta().GetBackendStorage()
	if backendStorage == nil {
		return nil
	}

	// Only one of the backend storage can be set.
	if backendStorage.backendStorageCount() > 1 {
		return fmt.Errorf("only one of the backend storage can be set")
	}

	return nil
}

func (in *GreptimeDBCluster) validateDatanode() error {
	if err := validateTomlConfig(in.GetDatanode().GetConfig()); err != nil {
		return fmt.Errorf("invalid datanode toml config: '%v'", err)
	}
	return nil
}

func (in *GreptimeDBCluster) validateFlownode() error {
	if err := validateTomlConfig(in.GetFlownode().GetConfig()); err != nil {
		return fmt.Errorf("invalid flownode toml config: '%v'", err)
	}
	return nil
}

// Validate checks the GreptimeDBStandalone and returns an error if it is invalid.
func (in *GreptimeDBStandalone) Validate() error {
	if in == nil {
		return nil
	}

	if err := validateTomlConfig(in.GetConfig()); err != nil {
		return fmt.Errorf("invalid standalone toml config: '%v'", err)
	}

	if wal := in.GetWALProvider(); wal != nil {
		if err := validateWALProvider(wal); err != nil {
			return err
		}
	}

	if osp := in.GetObjectStorageProvider(); osp != nil {
		if err := validateObjectStorageProvider(osp); err != nil {
			return err
		}
	}

	return nil
}

// Check checks the GreptimeDBStandalone with other resources and returns an error if it is invalid.
func (in *GreptimeDBStandalone) Check(ctx context.Context, client client.Client) error {
	// Check if the TLS secret exists and contains the required keys.
	if secretName := in.GetTLS().GetSecretName(); secretName != "" {
		if err := checkTLSSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	// Check if the PodMonitor CRD exists.
	if in.GetPrometheusMonitor().IsEnablePrometheusMonitor() {
		if err := checkPodMonitorExists(ctx, client); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetS3Storage().GetSecretName(); secretName != "" {
		if err := checkS3CredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetOSSStorage().GetSecretName(); secretName != "" {
		if err := checkOSSCredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetGCSStorage().GetSecretName(); secretName != "" {
		if err := checkGCSCredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	if secretName := in.GetObjectStorageProvider().GetAZBlobStorage().GetSecretName(); secretName != "" {
		if err := checkAZBlobCredentialsSecret(ctx, client, in.GetNamespace(), secretName); err != nil {
			return err
		}
	}

	return nil
}

func validateTomlConfig(input string) error {
	if len(input) > 0 {
		data := make(map[string]interface{})
		err := toml.Unmarshal([]byte(input), &data)
		if err != nil {
			return err
		}
	}
	return nil
}

func validateWALProvider(input *WALProviderSpec) error {
	if input == nil {
		return nil
	}

	if input.GetRaftEngineWAL() != nil && input.GetKafkaWAL() != nil {
		return fmt.Errorf("only one of 'raftEngine' or 'kafka' can be set")
	}

	if fs := input.GetRaftEngineWAL().GetFileStorage(); fs != nil {
		if err := validateFileStorage(fs); err != nil {
			return err
		}
	}

	return nil
}

func validateObjectStorageProvider(input *ObjectStorageProviderSpec) error {
	if input == nil {
		return nil
	}

	if input.getSetObjectStorageCount() > 1 {
		return fmt.Errorf("only one storage provider can be set")
	}

	if fs := input.GetCacheFileStorage(); fs != nil {
		if err := validateFileStorage(fs); err != nil {
			return err
		}
	}

	return nil
}

func validateFileStorage(input *FileStorage) error {
	if input == nil {
		return nil
	}

	if input.GetName() == "" {
		return fmt.Errorf("name is required in file storage")
	}

	if input.GetMountPath() == "" {
		return fmt.Errorf("mountPath is required in file storage")
	}

	if input.GetSize() == "" {
		return fmt.Errorf("storageSize is required in file storage")
	}

	return nil
}

// checkTLSSecret checks if the secret exists and contains the required keys.
func checkTLSSecret(ctx context.Context, client client.Client, namespace, name string) error {
	return checkSecretData(ctx, client, namespace, name, []string{TLSCrtSecretKey, TLSKeySecretKey})
}

func checkGCSCredentialsSecret(ctx context.Context, client client.Client, namespace, name string) error {
	return checkSecretData(ctx, client, namespace, name, []string{ServiceAccountKey})
}

func checkOSSCredentialsSecret(ctx context.Context, client client.Client, namespace, name string) error {
	return checkSecretData(ctx, client, namespace, name, []string{AccessKeyIDSecretKey, AccessKeySecretSecretKey})
}

func checkS3CredentialsSecret(ctx context.Context, client client.Client, namespace, name string) error {
	return checkSecretData(ctx, client, namespace, name, []string{AccessKeyIDSecretKey, SecretAccessKeySecretKey})
}

func checkAZBlobCredentialsSecret(ctx context.Context, client client.Client, namespace, name string) error {
	return checkSecretData(ctx, client, namespace, name, []string{AccountName, AccountKey})
}

// checkPodMonitorExists checks if the PodMonitor CRD exists.
func checkPodMonitorExists(ctx context.Context, client client.Client) error {
	const (
		kind  = "podmonitors"
		group = "monitoring.coreos.com"
	)

	var crd apiextensionsv1.CustomResourceDefinition
	if err := client.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s.%s", kind, group)}, &crd); err != nil {
		return err
	}

	return nil
}

// checkSecretData checks if the secret exists and contains the required keys.
func checkSecretData(ctx context.Context, client client.Client, namespace, name string, keys []string) error {
	var secret corev1.Secret
	if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		return err
	}

	if secret.Data == nil {
		return fmt.Errorf("the data of secret '%s/%s' is empty", namespace, name)
	}

	for _, key := range keys {
		if _, ok := secret.Data[key]; !ok {
			return fmt.Errorf("secret '%s/%s' does not have key '%s'", namespace, name, key)
		}
	}

	return nil
}
