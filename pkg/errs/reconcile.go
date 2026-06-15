package errs

import (
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// HandleReconcileError will handle errors from reconcile handlers, which respects runtime errors.
func HandleReconcileError(err error, log *logrus.Entry) (ctrl.Result, error) {
	if err == nil {
		return ctrl.Result{}, nil
	}

	var rateLimit *domain.RateLimitError
	if errors.As(err, &rateLimit) {
		wait := domain.RateLimitRequeueAfter(rateLimit.RetryAfter)
		log.Warnf("rate limited by VngCloud API, requeue after %s (server hint: %s, %s %s)",
			wait, rateLimit.RetryAfter, rateLimit.Method, rateLimit.URL)
		return ctrl.Result{RequeueAfter: wait}, nil
	}

	var requeueNeededAfter *RequeueNeededAfter
	if errors.As(err, &requeueNeededAfter) {
		log.Info("requeue after duration: ", requeueNeededAfter.Duration(), ", reason: ", requeueNeededAfter.Reason())
		return ctrl.Result{RequeueAfter: requeueNeededAfter.Duration()}, nil
	}

	var requeueNeeded *RequeueNeeded
	if errors.As(err, &requeueNeeded) {
		log.Info("requeue immediately reason: ", requeueNeeded.Reason())
		return ctrl.Result{Requeue: true}, nil
	}

	log.Infof("requeue after 5 seconds + exponential back-off, reason: %v", err)
	time.Sleep(5 * time.Second)
	return ctrl.Result{}, err
}
