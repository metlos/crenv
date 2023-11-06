package crenv

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFindDifferences(t *testing.T) {
	t.Run("single type", func(t *testing.T) {
		t.Run("different counts", func(t *testing.T) {
			diff := findDifferences(
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
				},
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
				},
			)

			assert.Equal(t, "objects of type ConfigMap: arrays have a different number of elements", diff)
		})

		t.Run("same count", func(t *testing.T) {
			diff := findDifferences(
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
				},
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string]string{
							"a": "b",
							"c": "e",
						},
					},
				},
			)

			assert.Equal(t, "objects of type ConfigMap: /husa: {Data.map[c]: d != e}", diff)
		})

		t.Run("different objects, same count", func(t *testing.T) {
			diff := findDifferences(
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
				},
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna2",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string]string{
							"a": "b",
							"c": "e",
						},
					},
				},
			)

			assert.Equal(t, "objects of type ConfigMap: /kachna: {not found in the new set}, /kachna2: {not found in the old set}, /husa: {Data.map[c]: d != e}", diff)
		})
	})

	t.Run("multiple types", func(t *testing.T) {
		t.Run("different counts", func(t *testing.T) {
			diff := findDifferences(
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string][]byte{
							"a": []byte("b"),
							"c": []byte("d"),
						},
					},
				},
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
				},
			)

			assert.Equal(t, "objects of type Secret: arrays have a different number of elements", diff)
		})

		t.Run("same count", func(t *testing.T) {
			diff := findDifferences(
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string][]byte{
							"a": []byte("b"),
							"c": []byte("d"),
						},
					},
				},
				[]client.Object{
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name: "kachna",
						},
						Data: map[string]string{
							"a": "b",
							"c": "d",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name: "husa",
						},
						Data: map[string][]byte{
							"a": []byte("b"),
							"c": []byte("e"),
						},
					},
				},
			)

			assert.Equal(t, "objects of type Secret: /husa: {Data.map[c].slice[0]: 100 != 101}", diff)
		})
	})
}

func TestSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CREnv Test Suite")
}

var _ = Describe("BeforeEach", func() {
	It("doesn't modify ToCreate objects", func() {
		ts := TestSetup{
			ToCreate: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cm",
						Namespace: "default",
					},
				},
			},
		}

		cl := fake.NewClientBuilder().Build()

		ts.BeforeEach(context.TODO(), cl, nil)

		inCluster := &corev1.ConfigMap{}
		Expect(cl.Get(context.TODO(), client.ObjectKeyFromObject(ts.ToCreate[0]), inCluster)).To(Succeed())
		Expect(inCluster.ResourceVersion).NotTo(BeEmpty())
		Expect(ts.ToCreate[0].GetResourceVersion()).To(BeEmpty())

		ts.AfterEach(context.TODO())
	})
})
