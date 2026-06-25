package vksvngcloudvn

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/blang/semver/v4"
	"golang.org/x/sync/errgroup"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"sigs.k8s.io/yaml"

	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s/apis/crdhelpers"
)

// Constants
const (
	SchemaVersionLabelKey = "vks.vngcloud.vn/crd-schema-version"
)

// Embed all CRD YAML files
var (
	//go:embed crds/vks.vngcloud.vn_loadbalancerconfigs.yaml
	loadbalancerconfigsCRDBytes []byte

	//go:embed crds/vks.vngcloud.vn_nodesecuritygroups.yaml
	nodesecuritygroupsCRDBytes []byte

	//go:embed crds/vks.vngcloud.vn_vngcloudgloballoadbalancers.yaml
	vngcloudgloballoadbalancersCRDBytes []byte

	//go:embed crds/vks.vngcloud.vn_globalloadbalancerconfigs.yaml
	globalloadbalancerconfigsCRDBytes []byte

	//go:embed crds/gateway.vks.vngcloud.vn_vksgatewaypolicies.yaml
	vksgatewaypoliciesCRDBytes []byte

	//go:embed crds/gateway.vks.vngcloud.vn_vksbackendpolicies.yaml
	vksbackendpoliciesCRDBytes []byte

	//go:embed crds/gateway.vks.vngcloud.vn_vkshealthcheckpolicies.yaml
	vkshealthcheckpoliciesCRDBytes []byte

	//go:embed crds/gateway.vks.vngcloud.vn_vksroutepolicies.yaml
	vksroutepoliciesCRDBytes []byte
)

// CRDDefinition holds the name and embedded bytes of a CRD
type CRDDefinition struct {
	Name  string
	Bytes []byte
}

// GetAllCRDs returns all CRD definitions
func GetAllCRDs() []CRDDefinition {
	return []CRDDefinition{
		{Name: "loadbalancerconfigs.vks.vngcloud.vn", Bytes: loadbalancerconfigsCRDBytes},
		{Name: "nodesecuritygroups.vks.vngcloud.vn", Bytes: nodesecuritygroupsCRDBytes},
		{Name: "vngcloudgloballoadbalancers.vks.vngcloud.vn", Bytes: vngcloudgloballoadbalancersCRDBytes},
		{Name: "globalloadbalancerconfigs.vks.vngcloud.vn", Bytes: globalloadbalancerconfigsCRDBytes},
		{Name: "vksgatewaypolicies.gateway.vks.vngcloud.vn", Bytes: vksgatewaypoliciesCRDBytes},
		{Name: "vksbackendpolicies.gateway.vks.vngcloud.vn", Bytes: vksbackendpoliciesCRDBytes},
		{Name: "vkshealthcheckpolicies.gateway.vks.vngcloud.vn", Bytes: vkshealthcheckpoliciesCRDBytes},
		{Name: "vksroutepolicies.gateway.vks.vngcloud.vn", Bytes: vksroutepoliciesCRDBytes},
	}
}

// BuildCRD unmarshals a CRD from YAML bytes and adds version label
func BuildCRD(yamlBytes []byte, chartVersion string) (*apiextensionsv1.CustomResourceDefinition, error) {
	// 1. Unmarshal YAML
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(yamlBytes, crd); err != nil {
		return nil, err
	}

	// 2. Add version label
	if crd.Labels == nil {
		crd.Labels = make(map[string]string)
	}
	crd.Labels[SchemaVersionLabelKey] = chartVersion

	return crd, nil
}

// InstallSingleCRD installs or updates a single CRD
func InstallSingleCRD(clientset apiextensionsclient.Interface, def CRDDefinition, chartVersion string) error {
	crd, err := BuildCRD(def.Bytes, chartVersion)
	if err != nil {
		return err
	}

	return crdhelpers.CreateUpdateCRD(
		clientset,
		crd,
		crdhelpers.NewDefaultPoller(),
		SchemaVersionLabelKey,
		semver.MustParse(chartVersion),
	)
}

// InstallAllCRDs installs all CRDs in parallel
func InstallAllCRDs(clientset apiextensionsclient.Interface, chartVersion string, logger Logger) error {
	g, _ := errgroup.WithContext(context.Background())

	// Install each CRD in parallel
	for _, crdDef := range GetAllCRDs() {
		crdDef := crdDef // Capture for goroutine
		g.Go(func() error {
			if logger != nil {
				logger.Info("Installing CRD", "name", crdDef.Name)
			}
			return InstallSingleCRD(clientset, crdDef, chartVersion)
		})
	}

	// Wait for all to complete
	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to install CRDs: %w", err)
	}

	return nil
}

// Logger is a minimal interface for logging
type Logger interface {
	Info(msg string, keysAndValues ...interface{})
}
