package crenv

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestTest(t *testing.T) {
	cl, err := client.New(ctrl.GetConfigOrDie(), client.Options{})
	assert.NoError(t, err)

	ts := TestSetup{
		client: cl,
		MonitoredObjectTypes: []client.Object{
			&v1.ConfigMap{},
			&v1.Secret{},
		},
	}

	assert.NoError(t, ts.ensureMonitoredGvks())

	ul := unstructured.UnstructuredList{}
	ul.SetGroupVersionKind(ts.monitoredGvks[reflect.TypeOf(&v1.ConfigMap{})][0])

	assert.NoError(t, cl.List(context.Background(), &ul))

	assert.Greater(t, len(ul.Items), 0)

	ucm := ul.Items[0]

	data, err := ucm.MarshalJSON()
	assert.NoError(t, err)
	cm := &v1.ConfigMap{}
	assert.NoError(t, json.Unmarshal(data, cm))
	assert.NotEmpty(t, cm.Name)
	assert.NotEmpty(t, cm.Namespace)
}
