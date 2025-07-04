// Copyright 2022 Greptime Team
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

package deployers

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/GreptimeTeam/greptimedb-operator/apis/v1alpha1"
	"github.com/GreptimeTeam/greptimedb-operator/controllers/common"
	"github.com/GreptimeTeam/greptimedb-operator/controllers/constant"
	"github.com/GreptimeTeam/greptimedb-operator/pkg/dbconfig"
	"github.com/GreptimeTeam/greptimedb-operator/pkg/deployer"
	"github.com/GreptimeTeam/greptimedb-operator/pkg/util"
	k8sutil "github.com/GreptimeTeam/greptimedb-operator/pkg/util/k8s"
)

var (
	defaultDialTimeout = 5 * time.Second
)

type EtcdMaintenanceBuilder func(etcdEndpoints []string) (clientv3.Maintenance, error)

type MetaDeployer struct {
	*CommonDeployer

	etcdMaintenanceBuilder func(etcdEndpoints []string) (clientv3.Maintenance, error)

	// If true, the meta will be in maintenance mode when creating cluster.
	maintenanceModeWhenCreateCluster bool
}

type MetaDeployerOption func(*MetaDeployer)

var _ deployer.Deployer = &MetaDeployer{}

func NewMetaDeployer(mgr ctrl.Manager, opts ...MetaDeployerOption) *MetaDeployer {
	md := &MetaDeployer{
		CommonDeployer:         NewFromManager(mgr),
		etcdMaintenanceBuilder: buildEtcdMaintenance,
	}

	for _, opt := range opts {
		opt(md)
	}

	return md
}

func WithEtcdMaintenanceBuilder(builder EtcdMaintenanceBuilder) func(*MetaDeployer) {
	return func(d *MetaDeployer) {
		d.etcdMaintenanceBuilder = builder
	}
}

func WithMaintenanceModeWhenCreateCluster(maintenanceModeWhenCreateCluster bool) func(*MetaDeployer) {
	return func(d *MetaDeployer) {
		d.maintenanceModeWhenCreateCluster = maintenanceModeWhenCreateCluster
	}
}

func (d *MetaDeployer) NewBuilder(crdObject client.Object) deployer.Builder {
	return &metaBuilder{
		CommonBuilder: d.NewCommonBuilder(crdObject, v1alpha1.MetaRoleKind),
	}
}

func (d *MetaDeployer) Generate(crdObject client.Object) ([]client.Object, error) {
	objects, err := d.NewBuilder(crdObject).
		BuildService().
		BuildConfigMap().
		BuildDeployment().
		BuildPodMonitor().
		SetControllerAndAnnotation().
		Generate()

	if err != nil {
		return nil, err
	}

	return objects, nil
}

func (d *MetaDeployer) PreSyncHooks() []deployer.Hook {
	var hooks []deployer.Hook
	hooks = append(hooks, d.checkEtcdService)
	return hooks
}

func (d *MetaDeployer) CheckAndUpdateStatus(ctx context.Context, highLevelObject client.Object) (bool, error) {
	cluster, err := d.GetCluster(highLevelObject)
	if err != nil {
		return false, err
	}

	var (
		deployment = new(appsv1.Deployment)

		objectKey = client.ObjectKey{
			Namespace: cluster.Namespace,
			Name:      common.ResourceName(cluster.Name, v1alpha1.MetaRoleKind),
		}
	)

	err = d.Get(ctx, objectKey, deployment)
	if errors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	cluster.Status.Meta.Replicas = *deployment.Spec.Replicas
	cluster.Status.Meta.ReadyReplicas = deployment.Status.ReadyReplicas
	cluster.Status.Meta.EtcdEndpoints = cluster.Spec.Meta.EtcdEndpoints

	ready := k8sutil.IsDeploymentReady(deployment)

	// It should meet the following conditions to turn on maintenance mode:
	// 1. The cluster is in starting phase that means the cluster is in the process of being created.
	// 2. The meta deployment is ready and the maintenance mode is not enabled.
	// 3. The `maintenanceModeWhenCreateCluster` is true in meta deployer options.
	if d.maintenanceModeWhenCreateCluster &&
		cluster.Status.ClusterPhase == v1alpha1.PhaseStarting &&
		ready && !cluster.Status.Meta.MaintenanceMode {
		// Turn on maintenance mode for metasrv.
		if err := common.SetMaintenanceMode(common.GetMetaHTTPServiceURL(cluster), true); err != nil {
			return false, err
		}
		cluster.Status.Meta.MaintenanceMode = true
	}

	if err := UpdateStatus(ctx, cluster, d.Client); err != nil {
		klog.Errorf("Failed to update status: %s", err)
	}

	return ready, nil
}

func (d *MetaDeployer) checkEtcdService(ctx context.Context, crdObject client.Object) error {
	cluster, err := d.GetCluster(crdObject)
	if err != nil {
		return err
	}

	if cluster.Spec.Meta == nil || !cluster.Spec.Meta.EnableCheckEtcdService {
		return nil
	}

	maintainer, err := d.etcdMaintenanceBuilder(cluster.Spec.Meta.EtcdEndpoints)
	if err != nil {
		return err
	}

	rsp, err := maintainer.Status(ctx, strings.Join(cluster.Spec.Meta.EtcdEndpoints, ","))
	if err != nil {
		return err
	}

	if len(rsp.Errors) != 0 {
		return fmt.Errorf("etcd service error: %v", rsp.Errors)
	}

	defer func() {
		etcdClient, ok := maintainer.(*clientv3.Client)
		if ok {
			etcdClient.Close()
		}
	}()

	return nil
}

func buildEtcdMaintenance(etcdEndpoints []string) (clientv3.Maintenance, error) {
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   etcdEndpoints,
		DialTimeout: defaultDialTimeout,
	})
	if err != nil {
		return nil, err
	}

	return etcdClient, nil
}

var _ deployer.Builder = &metaBuilder{}

type metaBuilder struct {
	*CommonBuilder
}

func (b *metaBuilder) BuildService() deployer.Builder {
	if b.Err != nil {
		return b
	}

	if b.Cluster.Spec.Meta == nil {
		return b
	}

	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: b.Cluster.Namespace,
			Name:      common.ResourceName(b.Cluster.Name, b.RoleKind),
			Labels: map[string]string{
				constant.GreptimeDBComponentName: common.ResourceName(b.Cluster.Name, b.RoleKind),
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				constant.GreptimeDBComponentName: common.ResourceName(b.Cluster.Name, b.RoleKind),
			},
			Ports: b.servicePorts(),
		},
	}

	b.Objects = append(b.Objects, svc)

	return b
}

func (b *metaBuilder) BuildDeployment() deployer.Builder {
	if b.Err != nil {
		return b
	}

	if b.Cluster.Spec.Meta == nil {
		return b
	}

	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.ResourceName(b.Cluster.Name, b.RoleKind),
			Namespace: b.Cluster.Namespace,
			Labels: map[string]string{
				constant.GreptimeDBComponentName: common.ResourceName(b.Cluster.Name, b.RoleKind),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: b.Cluster.Spec.Meta.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					constant.GreptimeDBComponentName: common.ResourceName(b.Cluster.Name, b.RoleKind),
				},
			},
			Template: *b.generatePodTemplateSpec(),
			Strategy: appsv1.DeploymentStrategy{
				Type:          appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: b.Cluster.Spec.Meta.RollingUpdate,
			},
		},
	}

	configData, err := dbconfig.FromCluster(b.Cluster, b.Cluster.GetMeta())
	if err != nil {
		b.Err = err
		return b
	}

	deployment.Spec.Template.Annotations = util.MergeStringMap(deployment.Spec.Template.Annotations,
		map[string]string{deployer.ConfigHash: util.CalculateConfigHash(configData)})

	b.Objects = append(b.Objects, deployment)

	return b
}

func (b *metaBuilder) BuildConfigMap() deployer.Builder {
	if b.Err != nil {
		return b
	}

	if b.Cluster.GetMeta() == nil {
		return b
	}

	cm, err := b.GenerateConfigMap(b.Cluster.GetMeta())
	if err != nil {
		b.Err = err
		return b
	}

	b.Objects = append(b.Objects, cm)

	return b
}

func (b *metaBuilder) BuildPodMonitor() deployer.Builder {
	if b.Err != nil {
		return b
	}

	if b.Cluster.Spec.Meta == nil {
		return b
	}

	if b.Cluster.Spec.PrometheusMonitor == nil || !b.Cluster.Spec.PrometheusMonitor.Enabled {
		return b
	}

	pm, err := b.GeneratePodMonitor(b.Cluster.Namespace, common.ResourceName(b.Cluster.Name, b.RoleKind))
	if err != nil {
		b.Err = err
		return b
	}

	b.Objects = append(b.Objects, pm)

	return b
}

func (b *metaBuilder) Generate() ([]client.Object, error) {
	return b.Objects, b.Err
}

func (b *metaBuilder) generatePodTemplateSpec() *corev1.PodTemplateSpec {
	podTemplateSpec := b.GeneratePodTemplateSpec(b.Cluster.Spec.Meta.Template)

	if len(b.Cluster.Spec.Meta.Template.MainContainer.Args) == 0 {
		// Setup main container args.
		podTemplateSpec.Spec.Containers[constant.MainContainerIndex].Args = append(b.generateMainContainerArgs(), b.Cluster.Spec.Meta.Template.MainContainer.ExtraArgs...)
	}

	podTemplateSpec.Spec.Containers[constant.MainContainerIndex].Ports = b.containerPorts()
	podTemplateSpec.Spec.Containers[constant.MainContainerIndex].Env = append(podTemplateSpec.Spec.Containers[constant.MainContainerIndex].Env, b.env(v1alpha1.MetaRoleKind)...)

	b.MountConfigDir(podTemplateSpec, common.ResourceName(b.Cluster.Name, b.RoleKind))

	if logging := b.Cluster.GetMeta().GetLogging(); logging != nil && !logging.IsOnlyLogToStdout() {
		b.AddLogsVolume(podTemplateSpec, logging.GetLogsDir())
	}

	if b.Cluster.GetMonitoring().IsEnabled() && b.Cluster.GetMonitoring().GetVector() != nil {
		b.AddVectorConfigVolume(podTemplateSpec)
		b.AddVectorSidecar(podTemplateSpec, v1alpha1.MetaRoleKind)
	}

	podTemplateSpec.Labels = util.MergeStringMap(podTemplateSpec.Labels, map[string]string{
		constant.GreptimeDBComponentName: common.ResourceName(b.Cluster.Name, b.RoleKind),
	})

	return podTemplateSpec
}

func (b *metaBuilder) generateMainContainerArgs() []string {
	return []string{
		"metasrv", "start",
		"--rpc-bind-addr", fmt.Sprintf("0.0.0.0:%d", b.Cluster.Spec.Meta.RPCPort),
		"--http-addr", fmt.Sprintf("0.0.0.0:%d", b.Cluster.Spec.Meta.HTTPPort),
		"--rpc-server-addr", fmt.Sprintf("$(%s):%d", deployer.EnvPodIP, b.Cluster.Spec.Meta.RPCPort),
		"--config-file", path.Join(constant.GreptimeDBConfigDir, constant.GreptimeDBConfigFileName),
	}
}

func (b *metaBuilder) servicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{
		{
			Name:     "rpc",
			Protocol: corev1.ProtocolTCP,
			Port:     b.Cluster.Spec.Meta.RPCPort,
		},
		{
			Name:     "http",
			Protocol: corev1.ProtocolTCP,
			Port:     b.Cluster.Spec.Meta.HTTPPort,
		},
	}
}

func (b *metaBuilder) containerPorts() []corev1.ContainerPort {
	return []corev1.ContainerPort{
		{
			Name:          "rpc",
			Protocol:      corev1.ProtocolTCP,
			ContainerPort: b.Cluster.Spec.Meta.RPCPort,
		},
		{
			Name:          "http",
			Protocol:      corev1.ProtocolTCP,
			ContainerPort: b.Cluster.Spec.Meta.HTTPPort,
		},
	}
}
