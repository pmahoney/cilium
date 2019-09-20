// Copyright 2016-2019 Authors of Cilium
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

package api

import (
	"github.com/cilium/cilium/pkg/labels"
)

// Rule is a policy rule which must be applied to all endpoints which match the
// labels contained in the endpointSelector
//
// Each rule is split into an ingress section which contains all rules
// applicable at ingress, and an egress section applicable at egress. For rule
// types such as `L4Rule` and `CIDR` which can be applied at both ingress and
// egress, both ingress and egress side have to either specifically allow the
// connection or one side has to be omitted.
//
// Either ingress, egress, or both can be provided. If both ingress and egress
// are omitted, the rule has no effect.
type Rule struct {
	// EndpointSelector selects all endpoints which should be subject to
	// this rule. Cannot be empty.
	//
	// +kubebuilder:validation:Required
	EndpointSelector EndpointSelector `json:"endpointSelector"`

	// Ingress is a list of IngressRule which are enforced at ingress.
	// If omitted or empty, this rule does not apply at ingress.
	//
	// +kubebuilder:validation:Optional
	Ingress []IngressRule `json:"ingress,omitempty"`

	// Egress is a list of EgressRule which are enforced at egress.
	// If omitted or empty, this rule does not apply at egress.
	//
	// +kubebuilder:validation:Optional
	Egress []EgressRule `json:"egress,omitempty"`

	// Labels is a list of optional strings which can be used to
	// re-identify the rule or to store metadata. It is possible to lookup
	// or delete strings based on labels. Labels are not required to be
	// unique, multiple rules can have overlapping or identical labels.
	//
	// +kubebuilder:validation:Optional
	Labels labels.LabelArray `json:"labels,omitempty"`

	// Description is a free form string, it can be used by the creator of
	// the rule to store human readable explanation of the purpose of this
	// rule. Rules cannot be identified by comment.
	//
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
}

// NewRule builds a new rule with no selector and no policy.
func NewRule() *Rule {
	return &Rule{}
}

// WithEndpointSelector configures the Rule with the specified selector.
func (r *Rule) WithEndpointSelector(es EndpointSelector) *Rule {
	r.EndpointSelector = es
	return r
}

// WithIngressRules configures the Rule with the specified rules.
func (r *Rule) WithIngressRules(rules []IngressRule) *Rule {
	r.Ingress = rules
	return r
}

// WithEgressRules configures the Rule with the specified rules.
func (r *Rule) WithEgressRules(rules []EgressRule) *Rule {
	r.Egress = rules
	return r
}

// WithLabels configures the Rule with the specified labels metadata.
func (r *Rule) WithLabels(labels labels.LabelArray) *Rule {
	r.Labels = labels
	return r
}

// WithDescription configures the Rule with the specified description metadata.
func (r *Rule) WithDescription(desc string) *Rule {
	r.Description = desc
	return r
}

// RequiresDerivative it return true if the rule has a derivative rule.
func (r *Rule) RequiresDerivative() bool {
	for _, rule := range r.Egress {
		if rule.RequiresDerivative() {
			return true
		}
	}
	return false
}

// CreateDerivative will return a new Rule with the new data based gather
// by the rules that autogenerated new Rule
func (r *Rule) CreateDerivative() (*Rule, error) {
	newRule := r.DeepCopy()
	newRule.Egress = []EgressRule{}

	for _, egressRule := range r.Egress {
		derivativeEgressRule, err := egressRule.CreateDerivative()
		if err != nil {
			return newRule, err
		}
		newRule.Egress = append(newRule.Egress, *derivativeEgressRule)
	}
	return newRule, nil
}
