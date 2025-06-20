package framework

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/eks-anywhere/internal/pkg/api"
	"github.com/aws/eks-anywhere/internal/test/cleanup"
	anywherev1 "github.com/aws/eks-anywhere/pkg/api/v1alpha1"
	"github.com/aws/eks-anywhere/pkg/executables"
	"github.com/aws/eks-anywhere/pkg/retrier"
	releasev1 "github.com/aws/eks-anywhere/release/api/v1alpha1"
	clusterf "github.com/aws/eks-anywhere/test/framework/cluster"
	"github.com/aws/eks-anywhere/test/framework/cluster/validations"
)

const (
	cloudstackDomainVar                         = "T_CLOUDSTACK_DOMAIN"
	cloudstackMultiLevelDomainVar               = "T_CLOUDSTACK_MULTILEVEL_DOMAIN"
	cloudstackZoneVar                           = "T_CLOUDSTACK_ZONE"
	cloudstackZone2Var                          = "T_CLOUDSTACK_ZONE_2"
	cloudstackZone3Var                          = "T_CLOUDSTACK_ZONE_3"
	cloudstackAccountVar                        = "T_CLOUDSTACK_ACCOUNT"
	cloudstackAccountForMultiLevelDomainVar     = "T_CLOUDSTACK_ACCOUNT_FOR_MULTILEVEL_DOMAIN"
	cloudstackNetworkVar                        = "T_CLOUDSTACK_NETWORK"
	cloudstackNetwork2Var                       = "T_CLOUDSTACK_NETWORK_2"
	cloudstackNetwork3Var                       = "T_CLOUDSTACK_NETWORK_3"
	cloudstackCredentialsVar                    = "T_CLOUDSTACK_CREDENTIALS"
	cloudstackCredentials2Var                   = "T_CLOUDSTACK_CREDENTIALS_2"
	cloudstackCredentials3Var                   = "T_CLOUDSTACK_CREDENTIALS_3"
	cloudstackCredentialsForMultiLevelDomainVar = "T_CLOUDSTACK_CREDENTIALS_FOR_MULTILEVEL_DOMAIN"
	cloudstackManagementServerVar               = "T_CLOUDSTACK_MANAGEMENT_SERVER"
	cloudstackManagementServer2Var              = "T_CLOUDSTACK_MANAGEMENT_SERVER_2"
	cloudstackManagementServer3Var              = "T_CLOUDSTACK_MANAGEMENT_SERVER_3"
	cloudstackSSHAuthorizedKeyVar               = "T_CLOUDSTACK_SSH_AUTHORIZED_KEY"
	cloudstackComputeOfferingLargeVar           = "T_CLOUDSTACK_COMPUTE_OFFERING_LARGE"
	cloudstackComputeOfferingLargerVar          = "T_CLOUDSTACK_COMPUTE_OFFERING_LARGER"
	cloudStackClusterIPPoolEnvVar               = "T_CLOUDSTACK_CLUSTER_IP_POOL"
	cloudStackCidrVar                           = "T_CLOUDSTACK_CIDR"
	podCidrVar                                  = "T_CLOUDSTACK_POD_CIDR"
	serviceCidrVar                              = "T_CLOUDSTACK_SERVICE_CIDR"
	cloudstackFeatureGateEnvVar                 = "CLOUDSTACK_PROVIDER"
	cloudstackB64EncodedSecretEnvVar            = "EKSA_CLOUDSTACK_B64ENCODED_SECRET"
)

var requiredCloudStackEnvVars = []string{
	cloudstackAccountVar,
	cloudstackAccountForMultiLevelDomainVar,
	cloudstackDomainVar,
	cloudstackMultiLevelDomainVar,
	cloudstackZoneVar,
	cloudstackZone2Var,
	cloudstackZone3Var,
	cloudstackCredentialsVar,
	cloudstackCredentials2Var,
	cloudstackCredentials3Var,
	cloudstackCredentialsForMultiLevelDomainVar,
	cloudstackAccountVar,
	cloudstackNetworkVar,
	cloudstackNetwork2Var,
	cloudstackNetwork3Var,
	cloudstackManagementServerVar,
	cloudstackManagementServer2Var,
	cloudstackManagementServer3Var,
	cloudstackSSHAuthorizedKeyVar,
	cloudstackComputeOfferingLargeVar,
	cloudstackComputeOfferingLargerVar,
	cloudStackCidrVar,
	podCidrVar,
	serviceCidrVar,
	cloudstackFeatureGateEnvVar,
	cloudstackB64EncodedSecretEnvVar,
}

type CloudStack struct {
	t                 *testing.T
	fillers           []api.CloudStackFiller
	clusterFillers    []api.ClusterFiller
	cidr              string
	podCidr           string
	serviceCidr       string
	cmkClient         *executables.Cmk
	devRelease        *releasev1.EksARelease
	templatesRegistry *templateRegistry
}

type CloudStackOpt func(*CloudStack)

func UpdateLargerCloudStackComputeOffering() api.CloudStackFiller {
	return api.WithCloudStackStringFromEnvVar(cloudstackComputeOfferingLargerVar, api.WithCloudStackComputeOfferingForAllMachines)
}

// UpdateAddCloudStackAz4 add availability zone 4 to the cluster spec.
func UpdateAddCloudStackAz4() api.CloudStackFiller {
	return api.WithCloudStackAzFromEnvVars(cloudstackAccountForMultiLevelDomainVar, cloudstackMultiLevelDomainVar, cloudstackZoneVar, cloudstackCredentialsForMultiLevelDomainVar, cloudstackNetworkVar,
		cloudstackManagementServerVar, api.WithCloudStackAz)
}

// UpdateAddCloudStackAz3 add availability zone 3 to the cluster spec.
func UpdateAddCloudStackAz3() api.CloudStackFiller {
	return api.WithCloudStackAzFromEnvVars(cloudstackAccountVar, cloudstackDomainVar, cloudstackZone3Var, cloudstackCredentials3Var, cloudstackNetwork3Var,
		cloudstackManagementServer3Var, api.WithCloudStackAz)
}

func UpdateAddCloudStackAz2() api.CloudStackFiller {
	return api.WithCloudStackAzFromEnvVars(cloudstackAccountVar, cloudstackDomainVar, cloudstackZone2Var, cloudstackCredentials2Var, cloudstackNetwork2Var,
		cloudstackManagementServer2Var, api.WithCloudStackAz)
}

func UpdateAddCloudStackAz1() api.CloudStackFiller {
	return api.WithCloudStackAzFromEnvVars(cloudstackAccountVar, cloudstackDomainVar, cloudstackZoneVar, cloudstackCredentialsVar, cloudstackNetworkVar,
		cloudstackManagementServerVar, api.WithCloudStackAz)
}

func RemoveAllCloudStackAzs() api.CloudStackFiller {
	return api.RemoveCloudStackAzs()
}

// CloudStackCredentialsAz1 returns the value of the environment variable for cloudstackCredentialsVar.
func CloudStackCredentialsAz1() string {
	return os.Getenv(cloudstackCredentialsVar)
}

func NewCloudStack(t *testing.T, opts ...CloudStackOpt) *CloudStack {
	checkRequiredEnvVars(t, requiredCloudStackEnvVars)
	cmk := buildCmk(t)
	c := &CloudStack{
		t:         t,
		cmkClient: cmk,
		fillers: []api.CloudStackFiller{
			api.RemoveCloudStackAzs(),
			api.WithCloudStackAzFromEnvVars(cloudstackAccountVar, cloudstackDomainVar, cloudstackZoneVar, cloudstackCredentialsVar, cloudstackNetworkVar,
				cloudstackManagementServerVar, api.WithCloudStackAz),
			api.WithCloudStackStringFromEnvVar(cloudstackSSHAuthorizedKeyVar, api.WithCloudStackSSHAuthorizedKey),
			api.WithCloudStackStringFromEnvVar(cloudstackComputeOfferingLargeVar, api.WithCloudStackComputeOfferingForAllMachines),
		},
	}

	c.cidr = os.Getenv(cloudStackCidrVar)
	c.podCidr = os.Getenv(podCidrVar)
	c.serviceCidr = os.Getenv(serviceCidrVar)
	c.templatesRegistry = &templateRegistry{cache: map[string]string{}, generator: c}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithCloudStackWorkerNodeGroup(name string, workerNodeGroup *WorkerNodeGroup, fillers ...api.CloudStackMachineConfigFiller) CloudStackOpt {
	return func(c *CloudStack) {
		c.fillers = append(c.fillers, cloudStackMachineConfig(name, fillers...))

		c.clusterFillers = append(c.clusterFillers, buildCloudStackWorkerNodeGroupClusterFiller(name, workerNodeGroup))
	}
}

// withKubeVersionAndOS returns a CloudStack Opt that adds API fillers to use a CloudStack template for
// the specified OS family and version (default if not provided), corresponding to a particular
// Kubernetes version, in addition to configuring all machine configs to use this OS family.
func withCloudStackKubeVersionAndOS(kubeVersion anywherev1.KubernetesVersion, os OS, release *releasev1.EksARelease) CloudStackOpt {
	return func(c *CloudStack) {
		c.fillers = append(c.fillers,
			c.templateForKubeVersionAndOS(kubeVersion, os, release),
		)
	}
}

// WithCloudStackRedhat128 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.28.
func WithCloudStackRedhat128() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube128, RedHat8, nil)
}

// WithCloudStackRedhat129 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.29.
func WithCloudStackRedhat129() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube129, RedHat8, nil)
}

// WithCloudStackRedhat130 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.30.
func WithCloudStackRedhat130() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube130, RedHat8, nil)
}

// WithCloudStackRedhat131 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.31.
func WithCloudStackRedhat131() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube131, RedHat8, nil)
}

// WithCloudStackRedhat9Kubernetes128 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.28.
func WithCloudStackRedhat9Kubernetes128() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube128, RedHat9, nil)
}

// WithCloudStackRedhat9Kubernetes129 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.29.
func WithCloudStackRedhat9Kubernetes129() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube129, RedHat9, nil)
}

// WithCloudStackRedhat9Kubernetes130 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.30.
func WithCloudStackRedhat9Kubernetes130() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube130, RedHat9, nil)
}

// WithCloudStackRedhat9Kubernetes131 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.31.
func WithCloudStackRedhat9Kubernetes131() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube131, RedHat9, nil)
}

// WithCloudStackRedhat9Kubernetes132 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.32.
func WithCloudStackRedhat9Kubernetes132() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube132, RedHat9, nil)
}

// WithCloudStackRedhat133 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.33.
func WithCloudStackRedhat133() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube133, RedHat8, nil)
}

// WithCloudStackRedhat9Kubernetes133 returns a function which can be invoked to configure the Cloudstack object to be compatible with K8s 1.33.
func WithCloudStackRedhat9Kubernetes133() CloudStackOpt {
	return withCloudStackKubeVersionAndOS(anywherev1.Kube133, RedHat9, nil)
}

func WithCloudStackFillers(fillers ...api.CloudStackFiller) CloudStackOpt {
	return func(c *CloudStack) {
		c.fillers = append(c.fillers, fillers...)
	}
}

func (c *CloudStack) Name() string {
	return "cloudstack"
}

func (c *CloudStack) Setup() {}

// UpdateKubeConfig customizes generated kubeconfig for the provider.
func (c *CloudStack) UpdateKubeConfig(content *[]byte, clusterName string) error {
	return nil
}

// ClusterConfigUpdates satisfies the test framework Provider.
func (c *CloudStack) ClusterConfigUpdates() []api.ClusterConfigFiller {
	controlPlaneIP, err := GetIP(c.cidr, ClusterIPPoolEnvVar)
	if err != nil {
		c.t.Fatalf("failed to get cluster ip for test environment: %v", err)
	}

	f := make([]api.ClusterFiller, 0, len(c.clusterFillers)+3)
	f = append(f, c.clusterFillers...)
	f = append(f,
		api.WithPodCidr(os.Getenv(podCidrVar)),
		api.WithServiceCidr(os.Getenv(serviceCidrVar)),
		api.WithControlPlaneEndpointIP(controlPlaneIP))

	return []api.ClusterConfigFiller{api.ClusterToConfigFiller(f...), api.CloudStackToConfigFiller(c.fillers...)}
}

// CleanupResources satisfies the test framework Provider.
func (c *CloudStack) CleanupResources(clusterName string) error {
	return cleanup.CloudstackTestResources(context.Background(), clusterName, false, false)
}

func (c *CloudStack) WithProviderUpgrade(fillers ...api.CloudStackFiller) ClusterE2ETestOpt {
	return func(e *ClusterE2ETest) {
		e.UpdateClusterConfig(api.CloudStackToConfigFiller(fillers...))
	}
}

func (c *CloudStack) WithProviderUpgradeGit(fillers ...api.CloudStackFiller) ClusterE2ETestOpt {
	return func(e *ClusterE2ETest) {
		e.UpdateClusterConfig(api.CloudStackToConfigFiller(fillers...))
	}
}

func (c *CloudStack) getControlPlaneIP() (string, error) {
	value, ok := os.LookupEnv(cloudStackClusterIPPoolEnvVar)
	var clusterIP string
	var err error
	if ok && value != "" {
		clusterIP, err = PopIPFromEnv(cloudStackClusterIPPoolEnvVar)
		if err != nil {
			c.t.Fatalf("failed to pop cluster ip from test environment: %v", err)
		}
	} else {
		clusterIP, err = GenerateUniqueIp(c.cidr)
		if err != nil {
			c.t.Fatalf("failed to generate ip for cloudstack %s: %v", c.cidr, err)
		}
	}
	return clusterIP, nil
}

func RequiredCloudstackEnvVars() []string {
	return requiredCloudStackEnvVars
}

func (c *CloudStack) WithNewCloudStackWorkerNodeGroup(name string, workerNodeGroup *WorkerNodeGroup, fillers ...api.CloudStackMachineConfigFiller) ClusterE2ETestOpt {
	return func(e *ClusterE2ETest) {
		e.UpdateClusterConfig(
			api.CloudStackToConfigFiller(cloudStackMachineConfig(name, fillers...)),
			api.ClusterToConfigFiller(buildCloudStackWorkerNodeGroupClusterFiller(name, workerNodeGroup)),
		)
	}
}

// WithNewWorkerNodeGroup returns an api.ClusterFiller that adds a new workerNodeGroupConfiguration and
// a corresponding CloudStackMachineConfig to the cluster config.
func (c *CloudStack) WithNewWorkerNodeGroup(name string, workerNodeGroup *WorkerNodeGroup) api.ClusterConfigFiller {
	return api.JoinClusterConfigFillers(
		api.CloudStackToConfigFiller(cloudStackMachineConfig(name)),
		api.ClusterToConfigFiller(buildCloudStackWorkerNodeGroupClusterFiller(name, workerNodeGroup)),
	)
}

// WithWorkerNodeGroupConfiguration returns an api.ClusterFiller that adds a new workerNodeGroupConfiguration item to the cluster config.
func (c *CloudStack) WithWorkerNodeGroupConfiguration(name string, workerNodeGroup *WorkerNodeGroup) api.ClusterConfigFiller {
	return api.ClusterToConfigFiller(buildCloudStackWorkerNodeGroupClusterFiller(name, workerNodeGroup))
}

func cloudStackMachineConfig(name string, fillers ...api.CloudStackMachineConfigFiller) api.CloudStackFiller {
	f := make([]api.CloudStackMachineConfigFiller, 0, len(fillers)+2)
	// Need to add these because at this point the default fillers that assign these
	// values to all machines have already ran
	f = append(f,
		api.WithCloudStackComputeOffering(os.Getenv(cloudstackComputeOfferingLargeVar)),
		api.WithCloudStackSSHKey(os.Getenv(cloudstackSSHAuthorizedKeyVar)),
	)
	f = append(f, fillers...)

	return api.WithCloudStackMachineConfig(name, f...)
}

// templateForKubeVersionAndOS returns a CloudStack filler for the given OS and Kubernetes version.
func (c *CloudStack) templateForKubeVersionAndOS(kubeVersion anywherev1.KubernetesVersion, os OS, release *releasev1.EksARelease) api.CloudStackFiller {
	var template string
	useBundlesOverride := getBundlesOverride() == "true"
	if release == nil {
		template = c.templateForDevRelease(kubeVersion, os, useBundlesOverride)
	} else {
		template = c.templatesRegistry.templateForRelease(c.t, release, kubeVersion, os, useBundlesOverride)
	}

	return api.WithCloudStackTemplateForAllMachines(template)
}

// Redhat128Template returns cloudstack filler for 1.28 RedHat.
func (c *CloudStack) Redhat128Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube128, RedHat8, nil)
}

// Redhat129Template returns cloudstack filler for 1.29 RedHat.
func (c *CloudStack) Redhat129Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube129, RedHat8, nil)
}

// Redhat130Template returns cloudstack filler for 1.30 RedHat.
func (c *CloudStack) Redhat130Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube130, RedHat8, nil)
}

// Redhat131Template returns cloudstack filler for 1.31 RedHat.
func (c *CloudStack) Redhat131Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube131, RedHat8, nil)
}

// Redhat132Template returns cloudstack filler for 1.32 RedHat.
func (c *CloudStack) Redhat132Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube132, RedHat8, nil)
}

// Redhat9Kubernetes128Template returns cloudstack filler for 1.28 RedHat.
func (c *CloudStack) Redhat9Kubernetes128Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube128, RedHat9, nil)
}

// Redhat9Kubernetes129Template returns cloudstack filler for 1.29 RedHat.
func (c *CloudStack) Redhat9Kubernetes129Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube129, RedHat9, nil)
}

// Redhat9Kubernetes130Template returns cloudstack filler for 1.30 RedHat.
func (c *CloudStack) Redhat9Kubernetes130Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube130, RedHat9, nil)
}

// Redhat9Kubernetes131Template returns cloudstack filler for 1.31 RedHat.
func (c *CloudStack) Redhat9Kubernetes131Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube131, RedHat9, nil)
}

// Redhat9Kubernetes132Template returns cloudstack filler for 1.32 RedHat.
func (c *CloudStack) Redhat9Kubernetes132Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube132, RedHat9, nil)
}

// Redhat133Template returns cloudstack filler for 1.33 RedHat.
func (c *CloudStack) Redhat133Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube133, RedHat8, nil)
}

// Redhat9Kubernetes133Template returns cloudstack filler for 1.33 RedHat.
func (c *CloudStack) Redhat9Kubernetes133Template() api.CloudStackFiller {
	return c.templateForKubeVersionAndOS(anywherev1.Kube133, RedHat9, nil)
}

func buildCloudStackWorkerNodeGroupClusterFiller(machineConfigName string, workerNodeGroup *WorkerNodeGroup) api.ClusterFiller {
	// Set worker node group ref to cloudstack machine config
	workerNodeGroup.MachineConfigKind = anywherev1.CloudStackMachineConfigKind
	workerNodeGroup.MachineConfigName = machineConfigName
	return workerNodeGroup.ClusterFiller()
}

// ClusterStateValidations returns a list of provider specific validations.
func (c *CloudStack) ClusterStateValidations() []clusterf.StateValidation {
	return []clusterf.StateValidation{
		clusterf.RetriableStateValidation(
			retrier.NewWithMaxRetries(60, 5*time.Second),
			validations.ValidateAvailabilityZones,
		),
	}
}

// WithKubeVersionAndOS returns a cluster config filler that sets the cluster kube version and the right template for all
// cloudstack machine configs.
func (c *CloudStack) WithKubeVersionAndOS(kubeVersion anywherev1.KubernetesVersion, os OS, release *releasev1.EksARelease, _ ...string) api.ClusterConfigFiller {
	return api.JoinClusterConfigFillers(
		api.ClusterToConfigFiller(api.WithKubernetesVersion(kubeVersion)),
		api.CloudStackToConfigFiller(
			c.templateForKubeVersionAndOS(kubeVersion, os, release),
		),
	)
}

// WithRedhat128 returns a cluster config filler that sets the kubernetes version of the cluster to 1.28
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat128() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube128, RedHat8, nil)
}

// WithRedhat129 returns a cluster config filler that sets the kubernetes version of the cluster to 1.29
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat129() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube129, RedHat8, nil)
}

// WithRedhat130 returns a cluster config filler that sets the kubernetes version of the cluster to 1.30
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat130() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube130, RedHat8, nil)
}

// WithRedhat131 returns a cluster config filler that sets the kubernetes version of the cluster to 1.31
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat131() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube131, RedHat8, nil)
}

// WithRedhat9Kubernetes128 returns a cluster config filler that sets the kubernetes version of the cluster to 1.28
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat9Kubernetes128() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube128, RedHat9, nil)
}

// WithRedhat9Kubernetes129 returns a cluster config filler that sets the kubernetes version of the cluster to 1.29
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat9Kubernetes129() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube129, RedHat9, nil)
}

// WithRedhat9Kubernetes130 returns a cluster config filler that sets the kubernetes version of the cluster to 1.30
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat9Kubernetes130() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube130, RedHat9, nil)
}

// WithRedhat9Kubernetes131 returns a cluster config filler that sets the kubernetes version of the cluster to 1.31
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat9Kubernetes131() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube131, RedHat9, nil)
}

// WithRedhat9Kubernetes132 returns a cluster config filler that sets the kubernetes version of the cluster to 1.32
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat9Kubernetes132() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube132, RedHat9, nil)
}

// WithRedhat133 returns a cluster config filler that sets the kubernetes version of the cluster to 1.33
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat133() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube133, RedHat8, nil)
}

// WithRedhat9Kubernetes133 returns a cluster config filler that sets the kubernetes version of the cluster to 1.33
// as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhat9Kubernetes133() api.ClusterConfigFiller {
	return c.WithKubeVersionAndOS(anywherev1.Kube133, RedHat9, nil)
}

// WithRedhatVersion returns a cluster config filler that sets the kubernetes version of the cluster to the k8s
// version provider, as well as the right redhat template for all CloudStackMachineConfigs.
func (c *CloudStack) WithRedhatVersion(version anywherev1.KubernetesVersion) api.ClusterConfigFiller {
	switch version {
	case anywherev1.Kube128:
		return c.WithRedhat128()
	case anywherev1.Kube129:
		return c.WithRedhat129()
	case anywherev1.Kube130:
		return c.WithRedhat130()
	case anywherev1.Kube131:
		return c.WithRedhat131()
	case anywherev1.Kube133:
		return c.WithRedhat133()
	default:
		return nil
	}
}

func (c *CloudStack) getDevRelease() *releasev1.EksARelease {
	if c.devRelease == nil {
		localDevRelease, err := localEksaCLIDevVersionRelease()
		if err != nil {
			c.t.Fatal(err)
		}
		c.t.Log("Using local eksa dev release:", localDevRelease.Version)
		c.devRelease = localDevRelease
	}

	return c.devRelease
}

func (c *CloudStack) templateForDevRelease(kubeVersion anywherev1.KubernetesVersion, os OS, useBundlesOverride bool) string {
	c.t.Helper()
	return c.templatesRegistry.templateForRelease(c.t, c.getDevRelease(), kubeVersion, os, useBundlesOverride)
}

// envVarForTemplate Looks for explicit configuration through an env var: "T_CLOUDSTACK_TEMPLATE_{osFamily}_{eks-d version}"
// eg: T_CLOUDSTACK_TEMPLATE_REDHAT_KUBERNETES_1_27_EKS_22.
func (c *CloudStack) envVarForTemplate(os OS, eksDName string) string {
	return fmt.Sprintf("T_CLOUDSTACK_TEMPLATE_%s_%s", strings.ToUpper(strings.ReplaceAll(string(os), "-", "_")), strings.ToUpper(strings.ReplaceAll(eksDName, "-", "_")))
}

// defaultNameForTemplate looks for a template: "{eks-d version}-{osFamily}"
// eg: kubernetes-1-27-eks-22-redhat.
func (c *CloudStack) defaultNameForTemplate(os OS, eksDName string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(eksDName), strings.ToLower(string(os)))
}

// defaultEnvVarForTemplate returns the value of the default template env vars: "T_CLOUDSTACK_TEMPLATE_{osFamily}_{kubeVersion}"
// eg. T_CLOUDSTACK_TEMPLATE_REDHAT_1_27.
func (c *CloudStack) defaultEnvVarForTemplate(os OS, kubeVersion anywherev1.KubernetesVersion) string {
	if osFamiliesForOS[os] == anywherev1.Bottlerocket {
		os = OS(strings.ReplaceAll(string(os), "bottlerocket", "br"))
	}
	return fmt.Sprintf("T_CLOUDSTACK_TEMPLATE_%s_%s", strings.ToUpper(strings.ReplaceAll(string(os), "-", "_")), strings.ReplaceAll(string(kubeVersion), ".", "_"))
}

// searchTemplate returns template name if the given template exists in the datacenter.
func (c *CloudStack) searchTemplate(ctx context.Context, template string) (string, error) {
	profile, ok := os.LookupEnv(cloudstackCredentialsVar)
	if !ok {
		return "", fmt.Errorf("required environment variable for CloudStack not set: %s", cloudstackCredentialsVar)
	}
	templateResource := anywherev1.CloudStackResourceIdentifier{
		Name: template,
	}
	template, err := c.cmkClient.SearchTemplate(context.Background(), profile, templateResource)
	if err != nil {
		return "", err
	}
	return template, nil
}
