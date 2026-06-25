package v1alpha1

// GetCommonStatus / GetCommonPolicyStatus expose the embedded status blocks so
// a generic Direct-policy validator can read and write Conditions + Ancestors
// uniformly across the four policy CRDs.

func (p *VKSGatewayPolicy) GetCommonStatus() *CommonStatus { return &p.Status.CommonStatus }
func (p *VKSGatewayPolicy) GetCommonPolicyStatus() *CommonPolicyStatus {
	return &p.Status.CommonPolicyStatus
}

func (p *VKSBackendPolicy) GetCommonStatus() *CommonStatus { return &p.Status.CommonStatus }
func (p *VKSBackendPolicy) GetCommonPolicyStatus() *CommonPolicyStatus {
	return &p.Status.CommonPolicyStatus
}

func (p *VKSHealthCheckPolicy) GetCommonStatus() *CommonStatus { return &p.Status.CommonStatus }
func (p *VKSHealthCheckPolicy) GetCommonPolicyStatus() *CommonPolicyStatus {
	return &p.Status.CommonPolicyStatus
}

func (p *VKSRoutePolicy) GetCommonStatus() *CommonStatus { return &p.Status.CommonStatus }
func (p *VKSRoutePolicy) GetCommonPolicyStatus() *CommonPolicyStatus {
	return &p.Status.CommonPolicyStatus
}
