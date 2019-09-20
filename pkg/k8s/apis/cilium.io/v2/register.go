// Copyright 2017 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v2

import (
	goerrors "errors"
	"fmt"
	"time"

	"sigs.k8s.io/yaml"

	k8sconst "github.com/cilium/cilium/pkg/k8s/apis/cilium.io"
	"github.com/cilium/cilium/pkg/option"

	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/hashicorp/go-version"
)

const (
	// CustomResourceDefinitionGroup is the name of the third party resource group
	CustomResourceDefinitionGroup = k8sconst.GroupName

	// CustomResourceDefinitionVersion is the current version of the resource
	CustomResourceDefinitionVersion = "v2"

	// CustomResourceDefinitionSchemaVersion is semver-conformant version of CRD schema
	// Used to determine if CRD needs to be updated in cluster
	CustomResourceDefinitionSchemaVersion = "1.15"

	// CustomResourceDefinitionSchemaVersionKey is key to label which holds the CRD schema version
	CustomResourceDefinitionSchemaVersionKey = "io.cilium.k8s.crd.schema.version"

	// CNPKindDefinition is the kind name for Cilium Network Policy
	CNPKindDefinition = "CiliumNetworkPolicy"

	fqdnNameRegex = `^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9\-]*[A-Za-z0-9])\.?$`

	fqdnPatternRegex = `^(([a-zA-Z0-9\*]|[a-zA-Z0-9\*][a-zA-Z0-9\-\*]*[a-zA-Z0-9\*])\.)*([A-Za-z0-9\*]|[A-Za-z0-9\*][A-Za-z0-9\-\*]*[A-Za-z0-9\*])\.?$`
)

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{
	Group:   CustomResourceDefinitionGroup,
	Version: CustomResourceDefinitionVersion,
}

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

var (
	// SchemeBuilder is needed by DeepCopy generator.
	SchemeBuilder runtime.SchemeBuilder
	// localSchemeBuilder and AddToScheme will stay in k8s.io/kubernetes.
	localSchemeBuilder = &SchemeBuilder

	// AddToScheme adds all types of this clientset into the given scheme.
	// This allows composition of clientsets, like in:
	//
	//   import (
	//     "k8s.io/client-go/kubernetes"
	//     clientsetscheme "k8s.io/client-go/kuberentes/scheme"
	//     aggregatorclientsetscheme "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/scheme"
	//   )
	//
	//   kclientset, _ := kubernetes.NewForConfig(c)
	//   aggregatorclientsetscheme.AddToScheme(clientsetscheme.Scheme)
	AddToScheme = localSchemeBuilder.AddToScheme

	comparableCRDSchemaVersion *version.Version
)

func init() {
	comparableCRDSchemaVersion = version.Must(
		version.NewVersion(CustomResourceDefinitionSchemaVersion))

	// We only register manually written functions here. The registration of the
	// generated functions takes place in the generated files. The separation
	// makes the code compile even when the generated files are missing.
	localSchemeBuilder.Register(addKnownTypes)
}

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&CiliumNetworkPolicy{},
		&CiliumNetworkPolicyList{},
		&CiliumEndpoint{},
		&CiliumEndpointList{},
		&CiliumNode{},
		&CiliumNodeList{},
		&CiliumIdentity{},
		&CiliumIdentityList{},
	)

	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// CreateCustomResourceDefinitions creates our CRD objects in the kubernetes
// cluster
func CreateCustomResourceDefinitions(clientset apiextensionsclient.Interface) error {
	if err := createCNPCRD(clientset); err != nil {
		return err
	}

	if err := createCEPCRD(clientset); err != nil {
		return err
	}

	if err := createNodeCRD(clientset); err != nil {
		return err
	}

	if option.Config.IdentityAllocationMode == option.IdentityAllocationModeCRD {
		if err := createIdentityCRD(clientset); err != nil {
			return err
		}
	}

	return nil
}

// createCNPCRD creates and updates the CiliumNetworkPolicies CRD. It should be called
// on agent startup but is idempotent and safe to call again.
func createCNPCRD(clientset apiextensionsclient.Interface) error {
	var (
		// CustomResourceDefinitionSingularName is the singular name of custom resource definition
		CustomResourceDefinitionSingularName = "ciliumnetworkpolicy"

		// CustomResourceDefinitionPluralName is the plural name of custom resource definition
		CustomResourceDefinitionPluralName = "ciliumnetworkpolicies"

		// CustomResourceDefinitionShortNames are the abbreviated names to refer to this CRD's instances
		CustomResourceDefinitionShortNames = []string{"cnp", "ciliumnp"}

		// CustomResourceDefinitionKind is the Kind name of custom resource definition
		CustomResourceDefinitionKind = CNPKindDefinition

		CRDName = CustomResourceDefinitionPluralName + "." + SchemeGroupVersion.Group
	)

	crdBytes, err := examplesCrdsCiliumnetworkpoliciesYamlBytes()
	if err != nil {
		panic(err)
	}
	ciliumCRD := apiextensionsv1beta1.CustomResourceDefinition{}
	err = yaml.Unmarshal(crdBytes, &ciliumCRD)
	if err != nil {
		panic(err)
	}

	res := &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: CRDName,
			Labels: map[string]string{
				CustomResourceDefinitionSchemaVersionKey: CustomResourceDefinitionSchemaVersion,
			},
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   SchemeGroupVersion.Group,
			Version: SchemeGroupVersion.Version,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:     CustomResourceDefinitionPluralName,
				Singular:   CustomResourceDefinitionSingularName,
				ShortNames: CustomResourceDefinitionShortNames,
				Kind:       CustomResourceDefinitionKind,
			},
			Subresources: &apiextensionsv1beta1.CustomResourceSubresources{
				Status: &apiextensionsv1beta1.CustomResourceSubresourceStatus{},
			},
			Scope:      apiextensionsv1beta1.NamespaceScoped,
			Validation: ciliumCRD.Spec.Validation,
		},
	}

	return createUpdateCRD(clientset, "CiliumNetworkPolicy/v2", res)
}

// createCEPCRD creates and updates the CiliumEndpoint CRD. It should be called
// on agent startup but is idempotent and safe to call again.
func createCEPCRD(clientset apiextensionsclient.Interface) error {
	var (
		// CustomResourceDefinitionSingularName is the singular name of custom resource definition
		CustomResourceDefinitionSingularName = "ciliumendpoint"

		// CustomResourceDefinitionPluralName is the plural name of custom resource definition
		CustomResourceDefinitionPluralName = "ciliumendpoints"

		// CustomResourceDefinitionShortNames are the abbreviated names to refer to this CRD's instances
		CustomResourceDefinitionShortNames = []string{"cep", "ciliumep"}

		// CustomResourceDefinitionKind is the Kind name of custom resource definition
		CustomResourceDefinitionKind = "CiliumEndpoint"

		CRDName = CustomResourceDefinitionPluralName + "." + SchemeGroupVersion.Group
	)

	res := &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: CRDName,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   SchemeGroupVersion.Group,
			Version: SchemeGroupVersion.Version,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:     CustomResourceDefinitionPluralName,
				Singular:   CustomResourceDefinitionSingularName,
				ShortNames: CustomResourceDefinitionShortNames,
				Kind:       CustomResourceDefinitionKind,
			},
			AdditionalPrinterColumns: []apiextensionsv1beta1.CustomResourceColumnDefinition{
				{
					Name:        "Endpoint ID",
					Type:        "integer",
					Description: "Cilium endpoint id",
					JSONPath:    ".status.id",
				},
				{
					Name:        "Identity ID",
					Type:        "integer",
					Description: "Cilium identity id",
					JSONPath:    ".status.identity.id",
				},
				{
					Name:        "Ingress Enforcement",
					Type:        "boolean",
					Description: "Ingress enforcement in the endpoint",
					JSONPath:    ".status.policy.ingress.enforcing",
				},
				{
					Name:        "Egress Enforcement",
					Type:        "boolean",
					Description: "Egress enforcement in the endpoint",
					JSONPath:    ".status.policy.egress.enforcing",
				},
				{
					Name:        "Endpoint State",
					Type:        "string",
					Description: "Endpoint current state",
					JSONPath:    ".status.state",
				},
				{
					Name:        "IPv4",
					Type:        "string",
					Description: "Endpoint IPv4 address",
					JSONPath:    ".status.networking.addressing[0].ipv4",
				},
				{
					Name:        "IPv6",
					Type:        "string",
					Description: "Endpoint IPv6 address",
					JSONPath:    ".status.networking.addressing[0].ipv6",
				},
			},
			Subresources: &apiextensionsv1beta1.CustomResourceSubresources{
				Status: &apiextensionsv1beta1.CustomResourceSubresourceStatus{},
			},
			Scope:      apiextensionsv1beta1.NamespaceScoped,
			Validation: &cepCRV,
		},
	}

	return createUpdateCRD(clientset, "v2.CiliumEndpoint", res)
}

// createNodeCRD creates and updates the CiliumNode CRD. It should be called on
// agent startup but is idempotent and safe to call again.
func createNodeCRD(clientset apiextensionsclient.Interface) error {
	res := &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ciliumnodes." + SchemeGroupVersion.Group,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   SchemeGroupVersion.Group,
			Version: SchemeGroupVersion.Version,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:     "ciliumnodes",
				Singular:   "ciliumnode",
				ShortNames: []string{"cn"},
				Kind:       "CiliumNode",
			},
			Subresources: &apiextensionsv1beta1.CustomResourceSubresources{
				Status: &apiextensionsv1beta1.CustomResourceSubresourceStatus{},
			},
			Scope: apiextensionsv1beta1.ClusterScoped,
		},
	}

	return createUpdateCRD(clientset, "v2.CiliumNode", res)
}

// createIdentityCRD creates and updates the CiliumIdentity CRD. It should be
// called on agent startup but is idempotent and safe to call again.
func createIdentityCRD(clientset apiextensionsclient.Interface) error {

	var (
		// CustomResourceDefinitionSingularName is the singular name of custom resource definition
		CustomResourceDefinitionSingularName = "ciliumidentity"

		// CustomResourceDefinitionPluralName is the plural name of custom resource definition
		CustomResourceDefinitionPluralName = "ciliumidentities"

		// CustomResourceDefinitionShortNames are the abbreviated names to refer to this CRD's instances
		CustomResourceDefinitionShortNames = []string{"ciliumid"}

		// CustomResourceDefinitionKind is the Kind name of custom resource definition
		CustomResourceDefinitionKind = "CiliumIdentity"

		CRDName = CustomResourceDefinitionPluralName + "." + SchemeGroupVersion.Group
	)

	res := &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: CRDName,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   SchemeGroupVersion.Group,
			Version: SchemeGroupVersion.Version,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:     CustomResourceDefinitionPluralName,
				Singular:   CustomResourceDefinitionSingularName,
				ShortNames: CustomResourceDefinitionShortNames,
				Kind:       CustomResourceDefinitionKind,
			},
			Subresources: &apiextensionsv1beta1.CustomResourceSubresources{
				Status: &apiextensionsv1beta1.CustomResourceSubresourceStatus{},
			},
			Scope: apiextensionsv1beta1.ClusterScoped,
		},
	}

	return createUpdateCRD(clientset, "v2.CiliumIdentity", res)
}

// createUpdateCRD ensures the CRD object is installed into the k8s cluster. It
// will create or update the CRD and it's validation when needed
func createUpdateCRD(clientset apiextensionsclient.Interface, CRDName string, crd *apiextensionsv1beta1.CustomResourceDefinition) error {
	scopedLog := log.WithField("name", CRDName)

	clusterCRD, err := clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Get(crd.ObjectMeta.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		scopedLog.Info("Creating CRD (CustomResourceDefinition)...")
		clusterCRD, err = clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
		// This occurs when multiple agents race to create the CRD. Since another has
		// created it, it will also update it, hence the non-error return.
		if errors.IsAlreadyExists(err) {
			return nil
		}
	}
	if err != nil {
		return err
	}

	scopedLog.Debug("Checking if CRD (CustomResourceDefinition) needs update...")
	if needsUpdate(clusterCRD) {
		scopedLog.Info("Updating CRD (CustomResourceDefinition)...")
		// Update the CRD with the validation schema.
		err = wait.Poll(500*time.Millisecond, 60*time.Second, func() (bool, error) {
			clusterCRD, err = clientset.ApiextensionsV1beta1().
				CustomResourceDefinitions().Get(crd.ObjectMeta.Name, metav1.GetOptions{})

			if err != nil {
				return false, err
			}

			// This seems too permissive but we only get here if the version is
			// different per needsUpdate above. If so, we want to update on any
			// validation change including adding or removing validation.
			if needsUpdate(clusterCRD) {
				scopedLog.Debug("CRD validation is different, updating it...")
				clusterCRD.ObjectMeta.Labels = crd.ObjectMeta.Labels
				clusterCRD.Spec = crd.Spec
				_, err = clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Update(clusterCRD)
				if err == nil {
					return true, nil
				}
				scopedLog.WithError(err).Debug("Unable to update CRD validation")
				return false, err
			}

			return true, nil
		})
		if err != nil {
			scopedLog.WithError(err).Error("Unable to update CRD")
			return err
		}
	}

	// wait for the CRD to be established
	scopedLog.Debug("Waiting for CRD (CustomResourceDefinition) to be available...")
	err = wait.Poll(500*time.Millisecond, 60*time.Second, func() (bool, error) {
		crd, err := clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Get(crd.ObjectMeta.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			switch cond.Type {
			case apiextensionsv1beta1.Established:
				if cond.Status == apiextensionsv1beta1.ConditionTrue {
					return true, err
				}
			case apiextensionsv1beta1.NamesAccepted:
				if cond.Status == apiextensionsv1beta1.ConditionFalse {
					scopedLog.WithError(goerrors.New(cond.Reason)).Error("Name conflict for CRD")
					return false, err
				}
			}
		}
		return false, err
	})
	if err != nil {
		deleteErr := clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Delete(crd.ObjectMeta.Name, nil)
		if deleteErr != nil {
			return fmt.Errorf("unable to delete k8s %s CRD %s. Deleting CRD due: %s", CRDName, deleteErr, err)
		}
		return err
	}

	scopedLog.Info("CRD (CustomResourceDefinition) is installed and up-to-date")
	return nil
}

func needsUpdate(clusterCRD *apiextensionsv1beta1.CustomResourceDefinition) bool {

	if clusterCRD.Spec.Validation == nil {
		// no validation detected
		return true
	}
	v, ok := clusterCRD.Labels[CustomResourceDefinitionSchemaVersionKey]
	if !ok {
		// no schema version detected
		return true
	}
	clusterVersion, err := version.NewVersion(v)
	if err != nil || clusterVersion.LessThan(comparableCRDSchemaVersion) {
		// version in cluster is either unparsable or smaller than current version
		return true
	}
	return false
}

var (
	// cepCRV is a minimal validation for CEP objects. Since only the agent is
	// creating them, it is better to be permissive and have some data, if buggy,
	// than to have no data in k8s.
	cepCRV = apiextensionsv1beta1.CustomResourceValidation{
		OpenAPIV3Schema: &apiextensionsv1beta1.JSONSchemaProps{},
	}

	cnpCRV = apiextensionsv1beta1.CustomResourceValidation{
		OpenAPIV3Schema: &apiextensionsv1beta1.JSONSchemaProps{},
	}
)
