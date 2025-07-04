package upgradevalidations_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/eks-anywhere/internal/test"
	internalmocks "github.com/aws/eks-anywhere/internal/test/mocks"
	anywherev1 "github.com/aws/eks-anywhere/pkg/api/v1alpha1"
	"github.com/aws/eks-anywhere/pkg/cluster"
	"github.com/aws/eks-anywhere/pkg/config"
	"github.com/aws/eks-anywhere/pkg/constants"
	"github.com/aws/eks-anywhere/pkg/filewriter"
	filewritermocks "github.com/aws/eks-anywhere/pkg/filewriter/mocks"
	"github.com/aws/eks-anywhere/pkg/manifests"
	"github.com/aws/eks-anywhere/pkg/manifests/releases"
	mockproviders "github.com/aws/eks-anywhere/pkg/providers/mocks"
	"github.com/aws/eks-anywhere/pkg/providers/tinkerbell"
	tinkerbellmocks "github.com/aws/eks-anywhere/pkg/providers/tinkerbell/mocks"
	"github.com/aws/eks-anywhere/pkg/providers/tinkerbell/stack"
	stackmocks "github.com/aws/eks-anywhere/pkg/providers/tinkerbell/stack/mocks"
	"github.com/aws/eks-anywhere/pkg/types"
	"github.com/aws/eks-anywhere/pkg/validations"
	"github.com/aws/eks-anywhere/pkg/validations/mocks"
	"github.com/aws/eks-anywhere/pkg/validations/upgradevalidations"
)

const (
	kubeconfigFilePath = "./fakeKubeconfigFilePath"
)

var goodClusterResponse = []types.CAPICluster{{Metadata: types.Metadata{Name: testclustername}}}

func TestPreflightValidationsTinkerbell(t *testing.T) {
	tests := []struct {
		name                    string
		clusterVersion          string
		upgradeVersion          string
		getClusterResponse      []types.CAPICluster
		cpResponse              error
		workerResponse          error
		nodeResponse            error
		crdResponse             error
		wantErr                 error
		modifyFunc              func(s *cluster.Spec)
		modifyDatacenterFunc    func(s *anywherev1.TinkerbellDatacenterConfig)
		modifyMachineConfigFunc func(s *anywherev1.TinkerbellMachineConfig)
	}{
		{
			name:               "ValidationSucceeds",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
		},
		{
			name:               "ValidationFailsClusterDoesNotExist",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: []types.CAPICluster{{Metadata: types.Metadata{Name: "thisIsNotTheClusterYourLookingFor"}}},
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("couldn't find CAPI cluster object for cluster with name testcluster"),
		},
		{
			name:               "ValidationFailsNoClusters",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: []types.CAPICluster{},
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("no CAPI cluster objects present on workload cluster testcluster"),
		},
		{
			name:               "ValidationFailsCpNotReady",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         errors.New("control plane nodes are not ready"),
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("control plane nodes are not ready"),
		},
		{
			name:               "ValidationFailsWorkerNodesNotReady",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     errors.New("2 worker nodes are not ready"),
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("2 worker nodes are not ready"),
		},
		{
			name:               "ValidationFailsNodesNotReady",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       errors.New("node test-node is not ready, currently in Unknown state"),
			crdResponse:        nil,
			wantErr:            composeError("node test-node is not ready, currently in Unknown state"),
		},
		{
			name:               "ValidationFailsNoCrds",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        errors.New("error getting clusters crd: crd not found"),
			wantErr:            composeError("error getting clusters crd: crd not found"),
		},
		{
			name:               "ValidationFailsExplodingCluster",
			clusterVersion:     "1.28",
			upgradeVersion:     "1.30",
			getClusterResponse: []types.CAPICluster{{Metadata: types.Metadata{Name: "thisIsNotTheClusterYourLookingFor"}}},
			cpResponse:         errors.New("control plane nodes are not ready"),
			workerResponse:     errors.New("2 worker nodes are not ready"),
			nodeResponse:       errors.New("node test-node is not ready, currently in Unknown state"),
			crdResponse:        errors.New("error getting clusters crd: crd not found"),
			wantErr:            explodingClusterError,
		},
		{
			name:               "ValidationControlPlaneImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.controlPlaneConfiguration.endpoint is immutable"),
			modifyFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ControlPlaneConfiguration.Endpoint.Host = "2.3.4.5"
			},
		},
		{
			name:               "ValidationClusterNetworkPodsImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.clusterNetwork.Pods is immutable"),
			modifyFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ClusterNetwork.Pods = anywherev1.Pods{}
			},
		},
		{
			name:               "ValidationClusterNetworkServicesImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.clusterNetwork.Services is immutable"),
			modifyFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ClusterNetwork.Services = anywherev1.Services{}
			},
		},
		{
			name:               "ValidationManagementImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("management flag is immutable"),
			modifyFunc: func(s *cluster.Spec) {
				s.Cluster.SetManagedBy(fmt.Sprintf("%s-1", s.Cluster.ManagedBy()))
			},
		},
		{
			name:               "ValidationTinkerbellIPImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.tinkerbellIP is immutable; previous = 4.5.6.7, new = 1.2.3.4"),
			modifyDatacenterFunc: func(s *anywherev1.TinkerbellDatacenterConfig) {
				s.Spec.TinkerbellIP = "4.5.6.7"
			},
		},
		{
			name:               "ValidationOSImageURLMutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyDatacenterFunc: func(s *anywherev1.TinkerbellDatacenterConfig) {
				s.Spec.OSImageURL = "http://old-os-image-url"
			},
		},
		{
			name:               "ValidationHookImageURLImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.hookImagesURLPath is immutable. previous = http://old-hook-image-url, new = http://hook-image-url"),
			modifyDatacenterFunc: func(s *anywherev1.TinkerbellDatacenterConfig) {
				s.Spec.HookImagesURLPath = "http://old-hook-image-url"
			},
		},
		{
			name:               "ValidationSSHUsernameImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.Users[0].Name is immutable. Previous value myOldSshUsername,   New value mySshUsername"),
			modifyMachineConfigFunc: func(s *anywherev1.TinkerbellMachineConfig) {
				s.Spec.Users[0].Name = "myOldSshUsername"
			},
		},
		{
			name:               "ValidationSSHAuthorizedKeysImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.Users[0].SshAuthorizedKeys[0] is immutable. Previous value myOldSshAuthorizedKeys,   New value mySshAuthorizedKey"),
			modifyMachineConfigFunc: func(s *anywherev1.TinkerbellMachineConfig) {
				s.Spec.Users[0].SshAuthorizedKeys[0] = "myOldSshAuthorizedKeys"
			},
		},
		{
			name:               "ValidationHardwareSelectorImmutable",
			clusterVersion:     "1.30",
			upgradeVersion:     "1.30",
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.HardwareSelector is immutable. Previous value map[type:cp1],   New value map[type:cp]"),
			modifyMachineConfigFunc: func(s *anywherev1.TinkerbellMachineConfig) {
				s.Spec.HardwareSelector = map[string]string{
					"type": "cp1",
				}
			},
		},
	}

	defaultControlPlane := anywherev1.ControlPlaneConfiguration{
		Count: 1,
		Endpoint: &anywherev1.Endpoint{
			Host: "1.1.1.1",
		},
		MachineGroupRef: &anywherev1.Ref{
			Name: "test-cp",
			Kind: "TinkerbellMachineConfig",
		},
	}

	defaultDatacenterSpec := anywherev1.TinkerbellDatacenterConfig{
		Spec: anywherev1.TinkerbellDatacenterConfigSpec{
			TinkerbellIP:      "1.2.3.4",
			OSImageURL:        "http://os-image-url",
			HookImagesURLPath: "http://hook-image-url",
		},
		Status: anywherev1.TinkerbellDatacenterConfigStatus{},
	}

	defaultTinkerbellMachineConfigSpec := anywherev1.TinkerbellMachineConfig{
		Spec: anywherev1.TinkerbellMachineConfigSpec{
			HardwareSelector: map[string]string{
				"type": "cp",
			},
			OSFamily: "ubuntu",
			Users: []anywherev1.UserConfiguration{{
				Name:              "mySshUsername",
				SshAuthorizedKeys: []string{"mySshAuthorizedKey"},
			}},
		},
	}

	ver := anywherev1.EksaVersion("v0.22.0")

	clusterSpec := test.NewClusterSpec(func(s *cluster.Spec) {
		s.Cluster.Name = testclustername
		s.Cluster.Spec.ControlPlaneConfiguration = defaultControlPlane
		s.Cluster.Spec.DatacenterRef = anywherev1.Ref{
			Kind: anywherev1.TinkerbellDatacenterKind,
			Name: "tinkerbell test",
		}
		s.Cluster.Spec.ClusterNetwork = anywherev1.ClusterNetwork{
			Pods: anywherev1.Pods{
				CidrBlocks: []string{
					"1.2.3.4/5",
				},
			},
			Services: anywherev1.Services{
				CidrBlocks: []string{
					"1.2.3.4/6",
				},
			},
		}
		s.Cluster.Spec.EksaVersion = &ver
	})

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			workloadCluster := &types.Cluster{}
			ctx := context.Background()
			workloadCluster.KubeconfigFile = kubeconfigFilePath
			workloadCluster.Name = testclustername

			mockCtrl := gomock.NewController(t)
			k := mocks.NewMockKubectlClient(mockCtrl)
			kubectl := tinkerbellmocks.NewMockProviderKubectlClient(mockCtrl)
			docker := stackmocks.NewMockDocker(mockCtrl)
			helm := stackmocks.NewMockHelm(mockCtrl)
			writer := filewritermocks.NewMockFileWriter(mockCtrl)
			tlsValidator := mocks.NewMockTlsValidator(mockCtrl)
			version := anywherev1.EksaVersion("v0.22.0")
			provider := newProvider(defaultDatacenterSpec, givenTinkerbellMachineConfigs(t), clusterSpec.Cluster, writer, docker, helm, kubectl, false)
			opts := &validations.Opts{
				Kubectl:           k,
				Spec:              clusterSpec,
				WorkloadCluster:   workloadCluster,
				ManagementCluster: workloadCluster,
				Provider:          provider,
				TLSValidator:      tlsValidator,
				CliVersion:        string(version),
				ManifestReader:    addManifestReaderMock(t, version),
			}

			clusterSpec.Cluster.Spec.KubernetesVersion = anywherev1.KubernetesVersion(tc.upgradeVersion)
			existingClusterSpec := clusterSpec.DeepCopy()
			existingClusterSpec.Cluster.Spec.KubernetesVersion = anywherev1.KubernetesVersion(tc.clusterVersion)
			existingProviderSpec := defaultDatacenterSpec.DeepCopy()
			existingMachineConfigSpec := defaultTinkerbellMachineConfigSpec.DeepCopy()
			if tc.modifyFunc != nil {
				tc.modifyFunc(existingClusterSpec)
			}
			if tc.modifyDatacenterFunc != nil {
				tc.modifyDatacenterFunc(existingProviderSpec)
			}
			if tc.modifyMachineConfigFunc != nil {
				tc.modifyMachineConfigFunc(existingMachineConfigSpec)
			}

			kubectl.EXPECT().GetEksaCluster(ctx, workloadCluster, clusterSpec.Cluster.Name).Return(existingClusterSpec.Cluster, nil).MaxTimes(1)

			kubectl.EXPECT().GetEksaTinkerbellDatacenterConfig(ctx, clusterSpec.Cluster.Spec.DatacenterRef.Name, gomock.Any(), gomock.Any()).Return(existingProviderSpec, nil).MaxTimes(1)
			kubectl.EXPECT().GetEksaTinkerbellMachineConfig(ctx, clusterSpec.Cluster.Spec.ControlPlaneConfiguration.MachineGroupRef.Name, gomock.Any(), gomock.Any()).Return(existingMachineConfigSpec, nil).MaxTimes(1)
			k.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			k.EXPECT().ValidateControlPlaneNodes(ctx, workloadCluster, clusterSpec.Cluster.Name).Return(tc.cpResponse)
			k.EXPECT().ValidateWorkerNodes(ctx, workloadCluster.Name, workloadCluster.KubeconfigFile).Return(tc.workerResponse)
			k.EXPECT().ValidateNodes(ctx, kubeconfigFilePath).Return(tc.nodeResponse)
			k.EXPECT().ValidateClustersCRD(ctx, workloadCluster).Return(tc.crdResponse)
			k.EXPECT().GetClusters(ctx, workloadCluster).Return(tc.getClusterResponse, nil)
			k.EXPECT().GetEksaCluster(ctx, workloadCluster, clusterSpec.Cluster.Name).Return(existingClusterSpec.Cluster, nil).MaxTimes(5)
			upgradeValidations := upgradevalidations.New(opts)
			err := validations.ProcessValidationResults(upgradeValidations.PreflightValidations(ctx))
			if err != nil && err.Error() != tc.wantErr.Error() {
				t.Errorf("%s want err=%v\n got err=%v\n", tc.name, tc.wantErr, err)
			}
		})
	}
}

func givenTinkerbellMachineConfigs(t *testing.T) map[string]*anywherev1.TinkerbellMachineConfig {
	config, err := cluster.ParseConfigFromFile("./testdata/tinkerbell_clusterconfig.yaml")
	if err != nil {
		t.Fatalf("unable to get machine configs from file: %v", err)
	}

	return config.TinkerbellMachineConfigs
}

func newProvider(datacenterConfig anywherev1.TinkerbellDatacenterConfig, machineConfigs map[string]*anywherev1.TinkerbellMachineConfig, clusterConfig *anywherev1.Cluster, writer filewriter.FileWriter, docker stack.Docker, helm stack.Helm, kubectl tinkerbell.ProviderKubectlClient, forceCleanup bool) *tinkerbell.Provider {
	hardwareFile := "./testdata/hardware.csv"
	bmcTimeout := 5 * time.Minute
	provider, err := tinkerbell.NewProvider(
		&datacenterConfig,
		machineConfigs,
		clusterConfig,
		hardwareFile,
		writer,
		docker,
		helm,
		kubectl,
		"1.2.3.4",
		test.FakeNow,
		forceCleanup,
		false,
		bmcTimeout,
	)
	if err != nil {
		panic(err)
	}

	return provider
}

func TestPreflightValidationsVsphere(t *testing.T) {
	tests := []struct {
		name                   string
		clusterVersion         string
		upgradeVersion         string
		getClusterResponse     []types.CAPICluster
		cpResponse             error
		workerResponse         error
		nodeResponse           error
		crdResponse            error
		wantErr                error
		modifyExistingSpecFunc func(s *cluster.Spec)
		modifyDefaultSpecFunc  func(s *cluster.Spec)
		additionalKubectlMocks func(k *mocks.MockKubectlClient)
	}{
		{
			name:               "ValidationBottlerocketKC",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyDefaultSpecFunc: func(s *cluster.Spec) {
				s.VSphereMachineConfigs = map[string]*anywherev1.VSphereMachineConfig{
					"test": {
						Spec: anywherev1.VSphereMachineConfigSpec{
							OSFamily: anywherev1.Bottlerocket,
							Users: []anywherev1.UserConfiguration{{
								Name:              "mySshUsername",
								SshAuthorizedKeys: []string{"mySshAuthorizedKey"},
							}},
						},
					},
					"wnRef": {
						Spec: anywherev1.VSphereMachineConfigSpec{
							OSFamily: anywherev1.Bottlerocket,
							Users: []anywherev1.UserConfiguration{{
								Name:              "mySshUsername",
								SshAuthorizedKeys: []string{"mySshAuthorizedKey"},
							}},
						},
					},
				}
				s.Cluster.Spec.ControlPlaneConfiguration.KubeletConfiguration = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"clusterDNSIPs": []string{"ip1"},
						"kind":          "KubeletConfiguration",
					},
				}
				s.Cluster.Spec.WorkerNodeGroupConfigurations[0].KubeletConfiguration = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"clusterDNSIPs": []string{"ip1"},
						"kind":          "KubeletConfiguration",
					},
				}
				s.Cluster.Spec.WorkerNodeGroupConfigurations[0].MachineGroupRef = &anywherev1.Ref{
					Name: "wnRef",
				}
			},
		},
		{
			name:               "ValidationSucceeds",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
		},
		{
			name:               "ValidationFailsClusterDoesNotExist",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: []types.CAPICluster{{Metadata: types.Metadata{Name: "thisIsNotTheClusterYourLookingFor"}}},
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("couldn't find CAPI cluster object for cluster with name testcluster"),
		},
		{
			name:               "ValidationFailsNoClusters",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: []types.CAPICluster{},
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("no CAPI cluster objects present on workload cluster testcluster"),
		},
		{
			name:               "ValidationFailsCpNotReady",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         errors.New("control plane nodes are not ready"),
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("control plane nodes are not ready"),
		},
		{
			name:               "ValidationFailsWorkerNodesNotReady",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     errors.New("2 worker nodes are not ready"),
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("2 worker nodes are not ready"),
		},
		{
			name:               "ValidationFailsNodesNotReady",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       errors.New("node test-node is not ready, currently in Unknown state"),
			crdResponse:        nil,
			wantErr:            composeError("node test-node is not ready, currently in Unknown state"),
		},
		{
			name:               "ValidationFailsNoCrds",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        errors.New("error getting clusters crd: crd not found"),
			wantErr:            composeError("error getting clusters crd: crd not found"),
		},
		{
			name:               "ValidationFailsExplodingCluster",
			clusterVersion:     string(anywherev1.Kube128),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: []types.CAPICluster{{Metadata: types.Metadata{Name: "thisIsNotTheClusterYourLookingFor"}}},
			cpResponse:         errors.New("control plane nodes are not ready"),
			workerResponse:     errors.New("2 worker nodes are not ready"),
			nodeResponse:       errors.New("node test-node is not ready, currently in Unknown state"),
			crdResponse:        errors.New("error getting clusters crd: crd not found"),
			wantErr:            explodingClusterError,
		},
		{
			name:               "ValidationControlPlaneImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.controlPlaneConfiguration.endpoint is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ControlPlaneConfiguration.Endpoint.Host = "2.3.4.5"
			},
		},
		{
			name:               "ValidationAwsIamRegionImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("aws iam identity provider is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.AWSIamConfig.Spec.AWSRegion = "us-east-2"
			},
		},
		{
			name:               "ValidationAwsIamBackEndModeImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("aws iam identity provider is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.AWSIamConfig.Spec.BackendMode = append(s.AWSIamConfig.Spec.BackendMode, "us-east-2")
			},
		},
		{
			name:               "ValidationAwsIamPartitionImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("aws iam identity provider is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.AWSIamConfig.Spec.Partition = "partition2"
			},
		},
		{
			name:               "ValidationAwsIamNameImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("aws iam identity provider is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.IdentityProviderRefs[1] = anywherev1.Ref{
					Kind: anywherev1.AWSIamConfigKind,
					Name: "aws-iam2",
				}
			},
		},
		{
			name:               "ValidationAwsIamKindImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("aws iam identity provider is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.IdentityProviderRefs[0] = anywherev1.Ref{
					Kind: anywherev1.OIDCConfigKind,
					Name: "oidc",
				}
			},
		},
		{
			name:               "ValidationAwsIamKindImmutableSwapOrder",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.IdentityProviderRefs[1] = anywherev1.Ref{
					Kind: anywherev1.AWSIamConfigKind,
					Name: "aws-iam",
				}
				s.Cluster.Spec.IdentityProviderRefs[0] = anywherev1.Ref{
					Kind: anywherev1.OIDCConfigKind,
					Name: "oidc",
				}
			},
		},
		{
			name:               "ValidationGitOpsNamespaceImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("gitOps spec.flux.github.fluxSystemNamespace is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.GitOpsConfig.Spec.Flux.Github.FluxSystemNamespace = "new-namespace"
			},
		},
		{
			name:               "ValidationGitOpsBranchImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("gitOps spec.flux.github.branch is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.GitOpsConfig.Spec.Flux.Github.Branch = "new-branch"
			},
		},
		{
			name:               "ValidationGitOpsOwnerImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("gitOps spec.flux.github.owner is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.GitOpsConfig.Spec.Flux.Github.Owner = "new-owner"
			},
		},
		{
			name:               "ValidationGitOpsRepositoryImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("gitOps spec.flux.github.repository is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.GitOpsConfig.Spec.Flux.Github.Repository = "new-repository"
			},
		},
		{
			name:               "ValidationGitOpsPathImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("gitOps spec.flux.github.clusterConfigPath is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.GitOpsConfig.Spec.Flux.Github.ClusterConfigPath = "new-path"
			},
		},
		{
			name:               "ValidationGitOpsPersonalImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("gitOps spec.flux.github.personal is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.GitOpsConfig.Spec.Flux.Github.Personal = !s.GitOpsConfig.Spec.Flux.Github.Personal
			},
		},
		{
			name:               "ValidationOIDCClientIdMutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.OIDCConfig.Spec.ClientId = "new-client-id"
			},
		},
		{
			name:               "ValidationOIDCGroupsClaimMutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.OIDCConfig.Spec.GroupsClaim = "new-groups-claim"
			},
		},
		{
			name:               "ValidationOIDCGroupsPrefixMutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.OIDCConfig.Spec.GroupsPrefix = "new-groups-prefix"
			},
		},
		{
			name:               "ValidationOIDCIssuerUrlMutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.OIDCConfig.Spec.IssuerUrl = "new-issuer-url"
			},
		},
		{
			name:               "ValidationOIDCUsernameClaimMutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.OIDCConfig.Spec.UsernameClaim = "new-username-claim"
			},
		},
		{
			name:               "ValidationOIDCUsernamePrefixMutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.OIDCConfig.Spec.UsernamePrefix = "new-username-prefix"
			},
		},
		{
			name:               "ValidationOIDCRequiredClaimsMutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            nil,
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.OIDCConfig.Spec.RequiredClaims[0].Claim = "new-groups-claim"
			},
		},
		{
			name:               "ValidationClusterNetworkPodsImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.clusterNetwork.Pods is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ClusterNetwork.Pods = anywherev1.Pods{}
			},
		},
		{
			name:               "ValidationClusterNetworkServicesImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.clusterNetwork.Services is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ClusterNetwork.Services = anywherev1.Services{}
			},
		},
		{
			name:               "ValidationClusterNetworkDNSImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.clusterNetwork.DNS is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ClusterNetwork.DNS = anywherev1.DNS{}
			},
		},
		{
			name:               "ValidationProxyConfigurationImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("spec.proxyConfiguration is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ProxyConfiguration = &anywherev1.ProxyConfiguration{
					HttpProxy:  "httpproxy2",
					HttpsProxy: "httpsproxy2",
					NoProxy: []string{
						"noproxy3",
					},
				}
			},
		},
		{
			name:               "ValidationEtcdConfigPreviousSpecEmpty",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("adding or removing external etcd during upgrade is not supported"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ExternalEtcdConfiguration = nil
				s.Cluster.Spec.DatacenterRef = anywherev1.Ref{
					Kind: anywherev1.VSphereDatacenterKind,
					Name: "vsphere test",
				}
			},
		},
		{
			name:               "ValidationManagementImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("management flag is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.SetManagedBy(fmt.Sprintf("%s-1", s.Cluster.ManagedBy()))
			},
		},
		{
			name:               "ValidationManagementClusterNameImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("management cluster name is immutable"),
			modifyExistingSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ManagementCluster.Name = fmt.Sprintf("%s-1", s.Cluster.ManagedBy())
			},
			modifyDefaultSpecFunc: func(s *cluster.Spec) {
				s.Cluster.Spec.ManagementCluster.Name = fmt.Sprintf("%s-2", s.Cluster.ManagedBy())
			},
		},
	}

	defaultControlPlane := anywherev1.ControlPlaneConfiguration{
		Count: 1,
		Endpoint: &anywherev1.Endpoint{
			Host: "1.1.1.1",
		},
		MachineGroupRef: &anywherev1.Ref{
			Name: "test",
			Kind: "VSphereMachineConfig",
		},
	}

	defaultETCD := &anywherev1.ExternalEtcdConfiguration{
		Count: 3,
	}

	defaultDatacenterSpec := anywherev1.VSphereDatacenterConfig{
		Spec: anywherev1.VSphereDatacenterConfigSpec{
			Datacenter: "datacenter!!!",
			Network:    "network",
			Server:     "server",
			Thumbprint: "thumbprint",
			Insecure:   false,
		},
		Status: anywherev1.VSphereDatacenterConfigStatus{},
	}

	defaultGitOps := &anywherev1.GitOpsConfig{
		Spec: anywherev1.GitOpsConfigSpec{
			Flux: anywherev1.Flux{
				Github: anywherev1.Github{
					Owner:               "owner",
					Repository:          "repo",
					FluxSystemNamespace: "flux-system",
					Branch:              "main",
					ClusterConfigPath:   "clusters/" + testclustername,
					Personal:            false,
				},
			},
		},
	}

	defaultOIDC := &anywherev1.OIDCConfig{
		Spec: anywherev1.OIDCConfigSpec{
			ClientId:     "client-id",
			GroupsClaim:  "groups-claim",
			GroupsPrefix: "groups-prefix",
			IssuerUrl:    "issuer-url",
			RequiredClaims: []anywherev1.OIDCConfigRequiredClaim{{
				Claim: "claim",
				Value: "value",
			}},
			UsernameClaim:  "username-claim",
			UsernamePrefix: "username-prefix",
		},
	}

	defaultAWSIAM := &anywherev1.AWSIamConfig{
		Spec: anywherev1.AWSIamConfigSpec{
			AWSRegion: "us-east-1",
			MapRoles: []anywherev1.MapRoles{{
				RoleARN:  "roleARN",
				Username: "username",
				Groups:   []string{"group1", "group2"},
			}},
			MapUsers: []anywherev1.MapUsers{{
				UserARN:  "userARN",
				Username: "username",
				Groups:   []string{"group1", "group2"},
			}},
			Partition: "partition",
		},
	}

	ver := anywherev1.EksaVersion("v0.22.0")

	defaultClusterSpec := test.NewClusterSpec(func(s *cluster.Spec) {
		s.Cluster.Name = testclustername
		s.Cluster.Spec.ControlPlaneConfiguration = defaultControlPlane
		s.Cluster.Spec.ExternalEtcdConfiguration = defaultETCD
		s.Cluster.Spec.DatacenterRef = anywherev1.Ref{
			Kind: anywherev1.VSphereDatacenterKind,
			Name: "vsphere test",
		}
		s.Cluster.Spec.IdentityProviderRefs = []anywherev1.Ref{
			{
				Kind: anywherev1.AWSIamConfigKind,
				Name: "aws-iam",
			},
			{
				Kind: anywherev1.OIDCConfigKind,
				Name: "oidc",
			},
		}
		s.Cluster.Spec.GitOpsRef = &anywherev1.Ref{
			Kind: anywherev1.GitOpsConfigKind,
			Name: "gitops test",
		}
		s.Cluster.Spec.ClusterNetwork = anywherev1.ClusterNetwork{
			Pods: anywherev1.Pods{
				CidrBlocks: []string{
					"1.2.3.4/5",
				},
			},
			Services: anywherev1.Services{
				CidrBlocks: []string{
					"1.2.3.4/6",
				},
			},
			DNS: anywherev1.DNS{
				ResolvConf: &anywherev1.ResolvConf{Path: "file.conf"},
			},
		}
		s.Cluster.Spec.ProxyConfiguration = &anywherev1.ProxyConfiguration{
			HttpProxy:  "httpproxy",
			HttpsProxy: "httpsproxy",
			NoProxy: []string{
				"noproxy1",
				"noproxy2",
			},
		}
		s.Cluster.Spec.BundlesRef = &anywherev1.BundlesRef{
			Name:      "bundles-28",
			Namespace: constants.EksaSystemNamespace,
		}

		s.GitOpsConfig = defaultGitOps
		s.OIDCConfig = defaultOIDC
		s.AWSIamConfig = defaultAWSIAM
		s.Cluster.Spec.EksaVersion = &ver
	})

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			_, ctx, workloadCluster, _ := validations.NewKubectl(t)
			workloadCluster.KubeconfigFile = kubeconfigFilePath
			workloadCluster.Name = testclustername

			mockCtrl := gomock.NewController(t)
			k := mocks.NewMockKubectlClient(mockCtrl)
			tlsValidator := mocks.NewMockTlsValidator(mockCtrl)

			provider := mockproviders.NewMockProvider(mockCtrl)

			clusterSpec := defaultClusterSpec.DeepCopy()
			if tc.modifyDefaultSpecFunc != nil {
				tc.modifyDefaultSpecFunc(clusterSpec)
			}

			eksaReleaseV022 := test.EKSARelease()
			eksaReleaseV022.Name = "eksa-v0-22-0"
			eksaReleaseV022.Spec.Version = "eksa-v0-22-0"

			objects := []client.Object{eksaReleaseV022}
			version := anywherev1.EksaVersion("v0.22.0")
			opts := &validations.Opts{
				Kubectl:           k,
				Spec:              clusterSpec,
				WorkloadCluster:   workloadCluster,
				ManagementCluster: workloadCluster,
				Provider:          provider,
				TLSValidator:      tlsValidator,
				CliVersion:        string(version),
				KubeClient:        test.NewFakeKubeClient(objects...),
				ManifestReader:    addManifestReaderMock(t, version),
			}

			clusterSpec.Cluster.Spec.KubernetesVersion = anywherev1.KubernetesVersion(tc.upgradeVersion)
			existingClusterSpec := defaultClusterSpec.DeepCopy()
			existingClusterSpec.Cluster.Spec.KubernetesVersion = anywherev1.KubernetesVersion(tc.clusterVersion)
			existingProviderSpec := defaultDatacenterSpec.DeepCopy()
			if tc.modifyExistingSpecFunc != nil {
				tc.modifyExistingSpecFunc(existingClusterSpec)
			}

			provider.EXPECT().DatacenterConfig(clusterSpec).Return(existingProviderSpec).MaxTimes(1)
			provider.EXPECT().ValidateNewSpec(ctx, workloadCluster, clusterSpec).Return(nil).MaxTimes(1)
			k.EXPECT().GetEksaVSphereDatacenterConfig(ctx, clusterSpec.Cluster.Spec.DatacenterRef.Name, gomock.Any(), gomock.Any()).Return(existingProviderSpec, nil).MaxTimes(1)
			k.EXPECT().ValidateControlPlaneNodes(ctx, workloadCluster, clusterSpec.Cluster.Name).Return(tc.cpResponse)
			k.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			k.EXPECT().ValidateWorkerNodes(ctx, workloadCluster.Name, workloadCluster.KubeconfigFile).Return(tc.workerResponse)
			k.EXPECT().ValidateNodes(ctx, kubeconfigFilePath).Return(tc.nodeResponse)
			k.EXPECT().ValidateClustersCRD(ctx, workloadCluster).Return(tc.crdResponse)
			k.EXPECT().GetClusters(ctx, workloadCluster).Return(tc.getClusterResponse, nil)
			k.EXPECT().GetEksaCluster(ctx, workloadCluster, clusterSpec.Cluster.Name).Return(existingClusterSpec.Cluster, nil).MaxTimes(5)
			if opts.Spec.Cluster.IsManaged() {
				k.EXPECT().GetEksaCluster(ctx, workloadCluster, workloadCluster.Name).Return(existingClusterSpec.Cluster, nil).MaxTimes(4)
			}
			k.EXPECT().GetEksaGitOpsConfig(ctx, clusterSpec.Cluster.Spec.GitOpsRef.Name, gomock.Any(), gomock.Any()).Return(existingClusterSpec.GitOpsConfig, nil).MaxTimes(1)
			k.EXPECT().GetEksaOIDCConfig(ctx, clusterSpec.Cluster.Spec.IdentityProviderRefs[1].Name, gomock.Any(), gomock.Any()).Return(existingClusterSpec.OIDCConfig, nil).MaxTimes(1)
			k.EXPECT().GetEksaAWSIamConfig(ctx, clusterSpec.Cluster.Spec.IdentityProviderRefs[0].Name, gomock.Any(), gomock.Any()).Return(existingClusterSpec.AWSIamConfig, nil).MaxTimes(1)
			upgradeValidations := upgradevalidations.New(opts)
			err := validations.ProcessValidationResults(upgradeValidations.PreflightValidations(ctx))
			if err != nil && err.Error() != tc.wantErr.Error() {
				t.Errorf("%s want err=%v\n got err=%v\n", tc.name, tc.wantErr, err)
			}
		})
	}
}

func composeError(msgs ...string) *validations.ValidationError {
	var errs []string
	errs = append(errs, msgs...)
	return &validations.ValidationError{Errs: errs}
}

var explodingClusterError = composeError(
	"control plane nodes are not ready",
	"2 worker nodes are not ready",
	"node test-node is not ready, currently in Unknown state",
	"error getting clusters crd: crd not found",
	"couldn't find CAPI cluster object for cluster with name testcluster",
	"spec: Invalid value: \"1.30\": only +1 minor version skew is supported, minor version skew detected 2",
)

func TestPreFlightValidationsGit(t *testing.T) {
	tests := []struct {
		name               string
		clusterVersion     string
		upgradeVersion     string
		getClusterResponse []types.CAPICluster
		cpResponse         error
		workerResponse     error
		nodeResponse       error
		crdResponse        error
		wantErr            error
		modifyFunc         func(s *cluster.Spec)
	}{
		{
			name:               "ValidationFluxSshKeyAlgoImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("fluxConfig spec.fluxConfig.spec.git.sshKeyAlgorithm is immutable"),
			modifyFunc: func(s *cluster.Spec) {
				s.FluxConfig.Spec.Git.SshKeyAlgorithm = "rsa2"
			},
		},
		{
			name:               "ValidationFluxRepoUrlImmutable",
			clusterVersion:     string(anywherev1.Kube130),
			upgradeVersion:     string(anywherev1.Kube130),
			getClusterResponse: goodClusterResponse,
			cpResponse:         nil,
			workerResponse:     nil,
			nodeResponse:       nil,
			crdResponse:        nil,
			wantErr:            composeError("fluxConfig spec.fluxConfig.spec.git.repositoryUrl is immutable"),
			modifyFunc: func(s *cluster.Spec) {
				s.FluxConfig.Spec.Git.RepositoryUrl = "test2"
			},
		},
	}
	defaultControlPlane := anywherev1.ControlPlaneConfiguration{
		Count: 1,
		Endpoint: &anywherev1.Endpoint{
			Host: "1.1.1.1",
		},
		MachineGroupRef: &anywherev1.Ref{
			Name: "test",
			Kind: "VSphereMachineConfig",
		},
	}

	defaultETCD := &anywherev1.ExternalEtcdConfiguration{
		Count: 3,
	}

	defaultDatacenterSpec := anywherev1.VSphereDatacenterConfig{
		Spec: anywherev1.VSphereDatacenterConfigSpec{
			Datacenter: "datacenter!!!",
			Network:    "network",
			Server:     "server",
			Thumbprint: "thumbprint",
			Insecure:   false,
		},
		Status: anywherev1.VSphereDatacenterConfigStatus{},
	}

	defaultFlux := &anywherev1.FluxConfig{
		Spec: anywherev1.FluxConfigSpec{
			Git: &anywherev1.GitProviderConfig{
				RepositoryUrl:   "test",
				SshKeyAlgorithm: "rsa",
			},
		},
	}
	defaultOIDC := &anywherev1.OIDCConfig{
		Spec: anywherev1.OIDCConfigSpec{
			ClientId:     "client-id",
			GroupsClaim:  "groups-claim",
			GroupsPrefix: "groups-prefix",
			IssuerUrl:    "issuer-url",
			RequiredClaims: []anywherev1.OIDCConfigRequiredClaim{{
				Claim: "claim",
				Value: "value",
			}},
			UsernameClaim:  "username-claim",
			UsernamePrefix: "username-prefix",
		},
	}

	defaultAWSIAM := &anywherev1.AWSIamConfig{
		Spec: anywherev1.AWSIamConfigSpec{
			AWSRegion: "us-east-1",
			MapRoles: []anywherev1.MapRoles{{
				RoleARN:  "roleARN",
				Username: "username",
				Groups:   []string{"group1", "group2"},
			}},
			MapUsers: []anywherev1.MapUsers{{
				UserARN:  "userARN",
				Username: "username",
				Groups:   []string{"group1", "group2"},
			}},
			Partition: "partition",
		},
	}

	ver := anywherev1.EksaVersion("v0.22.0")

	clusterSpec := test.NewClusterSpec(func(s *cluster.Spec) {
		s.Cluster.Name = testclustername
		s.Cluster.Spec.ControlPlaneConfiguration = defaultControlPlane
		s.Cluster.Spec.ExternalEtcdConfiguration = defaultETCD
		s.Cluster.Spec.DatacenterRef = anywherev1.Ref{
			Kind: anywherev1.VSphereDatacenterKind,
			Name: "vsphere test",
		}
		s.Cluster.Spec.IdentityProviderRefs = []anywherev1.Ref{
			{
				Kind: anywherev1.OIDCConfigKind,
				Name: "oidc",
			},
			{
				Kind: anywherev1.AWSIamConfigKind,
				Name: "aws-iam",
			},
		}
		s.Cluster.Spec.GitOpsRef = &anywherev1.Ref{
			Kind: anywherev1.FluxConfigKind,
			Name: "flux test",
		}
		s.Cluster.Spec.ClusterNetwork = anywherev1.ClusterNetwork{
			Pods: anywherev1.Pods{
				CidrBlocks: []string{
					"1.2.3.4/5",
				},
			},
			Services: anywherev1.Services{
				CidrBlocks: []string{
					"1.2.3.4/6",
				},
			},
			DNS: anywherev1.DNS{
				ResolvConf: &anywherev1.ResolvConf{Path: "file.conf"},
			},
		}
		s.Cluster.Spec.ProxyConfiguration = &anywherev1.ProxyConfiguration{
			HttpProxy:  "httpproxy",
			HttpsProxy: "httpsproxy",
			NoProxy: []string{
				"noproxy1",
				"noproxy2",
			},
		}

		s.Cluster.Spec.EksaVersion = &ver

		s.OIDCConfig = defaultOIDC
		s.AWSIamConfig = defaultAWSIAM
		s.FluxConfig = defaultFlux
	})

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			workloadCluster := &types.Cluster{}
			ctx := context.Background()
			workloadCluster.KubeconfigFile = kubeconfigFilePath
			workloadCluster.Name = testclustername

			mockCtrl := gomock.NewController(t)
			k := mocks.NewMockKubectlClient(mockCtrl)
			tlsValidator := mocks.NewMockTlsValidator(mockCtrl)
			cliConfig := &config.CliConfig{
				GitPrivateKeyFile:   "testdata/git_nonempty_private_key",
				GitSshKeyPassphrase: "test",
				GitKnownHostsFile:   "testdata/git_nonempty_ssh_known_hosts",
			}

			provider := mockproviders.NewMockProvider(mockCtrl)
			version := anywherev1.EksaVersion("v0.22.0")
			opts := &validations.Opts{
				Kubectl:           k,
				Spec:              clusterSpec,
				WorkloadCluster:   workloadCluster,
				ManagementCluster: workloadCluster,
				Provider:          provider,
				TLSValidator:      tlsValidator,
				CliConfig:         cliConfig,
				CliVersion:        string(version),
				ManifestReader:    addManifestReaderMock(t, version),
			}

			clusterSpec.Cluster.Spec.KubernetesVersion = anywherev1.KubernetesVersion(tc.upgradeVersion)
			existingClusterSpec := clusterSpec.DeepCopy()
			existingProviderSpec := defaultDatacenterSpec.DeepCopy()
			if tc.modifyFunc != nil {
				tc.modifyFunc(existingClusterSpec)
			}

			provider.EXPECT().DatacenterConfig(clusterSpec).Return(existingProviderSpec).MaxTimes(1)
			provider.EXPECT().ValidateNewSpec(ctx, workloadCluster, clusterSpec).Return(nil).MaxTimes(1)
			k.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			k.EXPECT().GetEksaVSphereDatacenterConfig(ctx, clusterSpec.Cluster.Spec.DatacenterRef.Name, gomock.Any(), gomock.Any()).Return(existingProviderSpec, nil).MaxTimes(1)
			k.EXPECT().ValidateControlPlaneNodes(ctx, workloadCluster, clusterSpec.Cluster.Name).Return(tc.cpResponse)
			k.EXPECT().ValidateWorkerNodes(ctx, workloadCluster.Name, workloadCluster.KubeconfigFile).Return(tc.workerResponse)
			k.EXPECT().ValidateNodes(ctx, kubeconfigFilePath).Return(tc.nodeResponse)
			k.EXPECT().ValidateClustersCRD(ctx, workloadCluster).Return(tc.crdResponse)
			k.EXPECT().GetClusters(ctx, workloadCluster).Return(tc.getClusterResponse, nil)
			k.EXPECT().GetEksaCluster(ctx, workloadCluster, clusterSpec.Cluster.Name).Return(existingClusterSpec.Cluster, nil).MaxTimes(5)
			k.EXPECT().GetEksaFluxConfig(ctx, clusterSpec.Cluster.Spec.GitOpsRef.Name, gomock.Any(), gomock.Any()).Return(existingClusterSpec.FluxConfig, nil).MaxTimes(1)
			k.EXPECT().GetEksaOIDCConfig(ctx, clusterSpec.Cluster.Spec.IdentityProviderRefs[0].Name, gomock.Any(), gomock.Any()).Return(existingClusterSpec.OIDCConfig, nil).MaxTimes(1)
			k.EXPECT().GetEksaAWSIamConfig(ctx, clusterSpec.Cluster.Spec.IdentityProviderRefs[1].Name, gomock.Any(), gomock.Any()).Return(existingClusterSpec.AWSIamConfig, nil).MaxTimes(1)
			upgradeValidations := upgradevalidations.New(opts)
			err := validations.ProcessValidationResults(upgradeValidations.PreflightValidations(ctx))
			if !reflect.DeepEqual(err, tc.wantErr) {
				t.Errorf("%s want err=%v\n got err=%v\n", tc.name, tc.wantErr, err)
			}
		})
	}
}

func addManifestReaderMock(t *testing.T, version anywherev1.EksaVersion) *manifests.Reader {
	ctrl := gomock.NewController(t)
	reader := internalmocks.NewMockReader(ctrl)
	releasesURL := releases.ManifestURL()

	releasesManifest := fmt.Sprintf(`apiVersion: anywhere.eks.amazonaws.com/v1alpha1
kind: Release
metadata:
  name: release-1
spec:
  releases:
  - bundleManifestUrl: "https://bundles/bundles.yaml"
    version: %s`, string(version))
	reader.EXPECT().ReadFile(releasesURL).Return([]byte(releasesManifest), nil)

	bundlesManifest := `apiVersion: anywhere.eks.amazonaws.com/v1alpha1
apiVersion: anywhere.eks.amazonaws.com/v1alpha1
kind: Bundles
metadata:
  annotations:
    anywhere.eks.amazonaws.com/signature: MEYCIQDgmE8oY9xUyFO3uOHRkpRWjTxoej8Wf7Ty5HQcbs9ouQIhANV2kylPXjcpLY2xu7vD6ktXqm7yrnLUgiehSdbL8JUJ
  name: bundles-1
spec:
  number: 1
  versionsBundles:
  - kubeversion: "1.30"
    endOfStandardSupport: "2026-12-31"`

	reader.EXPECT().ReadFile("https://bundles/bundles.yaml").Return([]byte(bundlesManifest), nil)

	return manifests.NewReader(reader)
}
