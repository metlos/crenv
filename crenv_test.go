package crenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
