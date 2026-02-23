package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

var _ = Describe("N8nPlugin Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		n8nplugin := &n8nv1alpha1.N8nPlugin{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind N8nPlugin")
			err := k8sClient.Get(ctx, typeNamespacedName, n8nplugin)
			if err != nil && errors.IsNotFound(err) {
				resource := &n8nv1alpha1.N8nPlugin{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: n8nv1alpha1.N8nPluginSpec{
						InstanceRef: n8nv1alpha1.N8nPluginInstanceReference{Name: "n8n"},
						PackageName: "n8n-nodes-test",
						Version:     "1.0.0",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &n8nv1alpha1.N8nPlugin{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if errors.IsNotFound(err) {
				return
			}
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance N8nPlugin")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &N8nPluginReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &n8nv1alpha1.N8nPlugin{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.ResolvedPackage).To(Equal("n8n-nodes-test@1.0.0"))

			ready := apimeta.FindStatusCondition(updated.Status.Conditions, pluginReadyConditionType)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal("InstanceNotFound"))
		})
	})
})
