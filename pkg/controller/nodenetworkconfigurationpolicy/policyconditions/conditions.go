package policyconditions

import (
	"context"
	"fmt"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	client "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	nmstatev1alpha1 "github.com/nmstate/kubernetes-nmstate/pkg/apis/nmstate/v1alpha1"
	enactmentconditions "github.com/nmstate/kubernetes-nmstate/pkg/controller/nodenetworkconfigurationpolicy/enactmentstatus/conditions"
)

var (
	log = logf.Log.WithName("policyconditions")
)

func setPolicyProgressing(conditions *nmstatev1alpha1.ConditionList, message string) {
	log.Info("setPolicyProgressing")
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionDegraded,
		corev1.ConditionUnknown,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionConfigurationProgressing,
		"",
	)
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionAvailable,
		corev1.ConditionUnknown,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionConfigurationProgressing,
		message,
	)
}

func setPolicySuccess(conditions *nmstatev1alpha1.ConditionList, message string) {
	log.Info("setPolicySuccess")
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionDegraded,
		corev1.ConditionFalse,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionSuccessfullyConfigured,
		"",
	)
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionAvailable,
		corev1.ConditionTrue,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionSuccessfullyConfigured,
		message,
	)
}

func setPolicyNotMatching(conditions *nmstatev1alpha1.ConditionList, message string) {
	log.Info("setPolicyNotMatching")
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionDegraded,
		corev1.ConditionFalse,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionConfigurationNoMatchingNode,
		message,
	)
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionAvailable,
		corev1.ConditionTrue,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionConfigurationNoMatchingNode,
		message,
	)
}

func setPolicyFailedToConfigure(conditions *nmstatev1alpha1.ConditionList, message string) {
	log.Info("setPolicyFailedToConfigure")
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionDegraded,
		corev1.ConditionTrue,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionFailedToConfigure,
		message,
	)
	conditions.Set(
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionAvailable,
		corev1.ConditionFalse,
		nmstatev1alpha1.NodeNetworkConfigurationPolicyConditionFailedToConfigure,
		"",
	)
}

func Update(cli client.Client, policyKey types.NamespacedName) error {
	logger := log.WithValues("policy", policyKey.Name)
	// On conflict we need to re-retrieve enactments since the
	// conflict can denote that the calculated policy conditions
	// are now not accurate.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		policy := &nmstatev1alpha1.NodeNetworkConfigurationPolicy{}
		err := cli.Get(context.TODO(), policyKey, policy)
		if err != nil {
			return errors.Wrap(err, "getting policy failed")
		}

		enactments := nmstatev1alpha1.NodeNetworkConfigurationEnactmentList{}
		policyLabelFilter := client.MatchingLabels{nmstatev1alpha1.EnactmentPolicyLabel: policy.Name}
		err = cli.List(context.TODO(), &enactments, policyLabelFilter)
		if err != nil {
			return errors.Wrap(err, "getting enactments failed")
		}

		nodes := corev1.NodeList{}
		err = cli.List(context.TODO(), &nodes)
		if err != nil {
			return errors.Wrap(err, "getting nodes failed")
		}
		numberOfReadyNodes := 0
		for _, node := range nodes.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady &&
					condition.Status == corev1.ConditionTrue {
					numberOfReadyNodes += 1
				}
			}
		}

		// Let's get conditions with true status count
		enactmentsCount := enactmentconditions.Count(enactments)

		numberOfFinishedEnactments := enactmentsCount.Available() + enactmentsCount.Failed() + enactmentsCount.NotMatching()

		logger.Info(fmt.Sprintf("enactments count: %s", enactmentsCount))
		if numberOfFinishedEnactments < numberOfReadyNodes {
			setPolicyProgressing(&policy.Status.Conditions, fmt.Sprintf("Policy is progressing %d/%d nodes finished", numberOfFinishedEnactments, numberOfReadyNodes))
		} else {
			if enactmentsCount.Matching() == 0 {
				message := "Policy does not match any node"
				setPolicyNotMatching(&policy.Status.Conditions, message)
			} else if enactmentsCount.Failed() > 0 {
				message := fmt.Sprintf("%d/%d nodes failed to configure", enactmentsCount.Failed(), enactmentsCount.Matching())
				setPolicyFailedToConfigure(&policy.Status.Conditions, message)
			} else {
				message := fmt.Sprintf("%d/%d nodes successfully configured", enactmentsCount.Available(), enactmentsCount.Available())
				setPolicySuccess(&policy.Status.Conditions, message)
			}
		}

		err = cli.Status().Update(context.TODO(), policy)
		if err != nil {
			if apierrors.IsConflict(err) {
				logger.Info("conflict updating policy conditions, retrying")
			} else {
				logger.Error(err, "failed to update policy conditions")
			}
			return err
		}
		return nil
	})
}

func Reset(cli client.Client, policyKey types.NamespacedName) error {
	logger := log.WithValues("policy", policyKey.Name)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		policy := &nmstatev1alpha1.NodeNetworkConfigurationPolicy{}
		err := cli.Get(context.TODO(), policyKey, policy)
		if err != nil {
			return errors.Wrap(err, "getting policy failed")
		}
		policy.Status.Conditions = nmstatev1alpha1.ConditionList{}
		err = cli.Status().Update(context.TODO(), policy)
		if err != nil {
			if apierrors.IsConflict(err) {
				logger.Info("conflict reseting policy conditions, retrying")
			} else {
				logger.Error(err, "failed to reset policy conditions")
			}
			return err
		}
		return nil
	})
}
