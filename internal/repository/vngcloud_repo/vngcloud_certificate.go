package vngcloud_repo

import (
	"context"

	"github.com/anngdinh/operator-helper/contexts"
	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// // --------------------------- Certificate ---------------------------

func (m *vngCloudRepository) ListCertificates(ctx context.Context) (*entityv2.ListCertificates, error) {
	logger := contexts.NewContext(ctx).Log()
	certs, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().ListCertificates(loadbalancerv2.NewListCertificatesRequest().AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - ListCertificates: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return certs, nil
}

func (m *vngCloudRepository) GetCertificateByID(ctx context.Context, certID string) (*entityv2.Certificate, error) {
	logger := contexts.NewContext(ctx).Log()
	cert, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().GetCertificateById(loadbalancerv2.NewGetCertificateByIdRequest(certID).AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - GetCertificateByID: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return cert, nil
}

func (m *vngCloudRepository) ImportCertificate(ctx context.Context, opt loadbalancerv2.ICreateCertificateRequest) (*entityv2.Certificate, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request import certificate", domain.RequestIcon)
	cert, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().CreateCertificate(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - ImportCertificate: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return cert, nil
}

func (m *vngCloudRepository) DeleteCertificate(ctx context.Context, certID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete certificate %s", domain.RequestIcon, certID)
	sdkErr := m.client.VLBGateway().V2().LoadBalancerService().DeleteCertificateById(loadbalancerv2.NewDeleteCertificateByIdRequest(certID).AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - DeleteCertificate: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}
