package crenv

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/go-logr/logr"
	"github.com/go-test/deep"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ExternalStateHandler implementation can be supplied to the TestSetup configuration and will be called
// at appropriate times to capture a state that should be preserved between test runs. See the docs of
// the individual methods to learn when they are called.
type ExternalStateHandler interface {
	// Capture is called during the TestSetup.BeforeEach to record and remember the state in its current form.
	Capture()
	// Reset is called immediately after Capture and is meant to set the captured state to its default form.
	Reset()
	// Restore is called during TestSetup.AfterEach and sets the state to the captured form.
	Restore()
}

// TestSetup is a struct using which one can express the desired state of the cluster so that a test can proceed.
// Use the BeforeEach and AfterEach methods in the corresponding Ginkgo test lifecycle methods to actually run it.
type TestSetup struct {
	// ToCreate is a list of objects to create during the BeforeEach call.
	ToCreate []client.Object
	// InCluster is a set of objects that exist in the cluster. This list contains only objects of the monitored object
	// types and updated during BeforeEach and ReconcileWithCluster methods.
	InCluster Objects
	// Behavior specifies additional hooks to call during BeforeEach.
	Behavior Behavior
	// MonitoredObjectTypes is the list of Kubernetes object types to monitor for changes when recording
	// the cluster state in TestSetup. Only the types of the objects from this list are used. The object types from
	// ToCreate are implicitly considered.
	MonitoredObjectTypes []client.Object
	// ExternalStateHandler is the handler that is able to capture and restore some external state that is important
	// to preserve between the test runs.
	ExternalStateHandler ExternalStateHandler
	// LogLevel the log level to use for the log messages from the TestSetup. These can be useful to debug timing and other
	// problems during the reconciliations.
	LogLevel int

	// ReconciliationChecks is map of function using which one can define how to infer that a reconciliation could have happened
	// from the state of the object. By default, if the object has a "status" field, then its presence is considered a "proof" of
	// reconciliation, otherwise no check is performed and the objects are considered reconciled. You can use this map to override
	// that default behavior. By specifying a custom function which should return true if the reconciliation hasn't happened yet.
	ReconciliationChecks map[schema.GroupKind]func(*unstructured.Unstructured) bool

	// There is no generic way of forcing a reconciliation on a CR. By default Crenv sets a random annotation to a random value
	// but that might not trigger reconciliation depending on the predicates that are set up by the reconcilers.
	// If modifying annotations does not trigger reconciliation for some CRD one can use this map to define a function that
	// will be used to modify the object in such a way that reconciliation should happen.
	ReconciliationTrigger map[schema.GroupKind]func(client.Object)

	// client to be used in all the methods, set during BeforeEach
	client client.Client

	monitoredGvks map[reflect.Type]gvkDescriptor
}

type gvkDescriptor struct {
	reconciliationCheck func(*unstructured.Unstructured) bool
	gvks                []schema.GroupVersionKind
}

// Objects is a holder of objects loaded from the cluster that provides some basic utility methods for a strongly typed lookup.
type Objects struct {
	objects []client.Object
}

// Behavior specifies the extra hooks that should be called during the TestSetup.BeforeEach.
type Behavior struct {
	// BeforeObjectsCreated is called right before the objects from the TestSetup.ToCreate are created in the cluster.
	// This enables one to set up any specfic behavior of the controllers etc. Note that this is only called after
	// BeforeEach confirmed that the cluster is empty and has reset the state using the ExternalStateHandler.
	BeforeObjectsCreated func()
	// AfterObjectsCreated is called after the objects from the TestSetup.ToCreate have been created. The supplied
	// Objects instance can be used to inspect what is in the cluster. After this method is called, a forced reconciliation
	// of all provided objects is executed unless DontTriggerReconcileAfterObjectsCreated is set to true.
	AfterObjectsCreated func(*Objects)
	// If true, no reconciliation is triggered after calling the AfterObjectsCreated function.
	DontTriggerReconcileAfterObjectsCreated bool
}

func (ts *TestSetup) ensureMonitoredGvks() error {
	if ts.monitoredGvks != nil {
		return nil
	}

	ts.monitoredGvks = map[reflect.Type]gvkDescriptor{}

	addToMonitoredGvks := func(obj client.Object) error {
		gvks, _, err := ts.client.Scheme().ObjectKinds(obj)
		if err != nil {
			return err
		}

		typ := reflect.TypeOf(obj).Elem()

		desc, initialized := ts.monitoredGvks[typ]
		if !initialized {
			var rc func(*unstructured.Unstructured) bool
			for _, gvk := range gvks {
				gk := gvk.GroupKind()
				rc := ts.ReconciliationChecks[gk]
				if rc != nil {
					break
				}
			}
			if rc == nil {
				// if the type as a field that's called "status" in JSON, we use its nullity to check if reconciliation might have happened.
				for i := 0; i < typ.NumField(); i++ {
					f := typ.Field(i)
					jsonTag := f.Tag.Get("json")
					if strings.HasPrefix(jsonTag, "status") {
						rc = func(o *unstructured.Unstructured) bool {
							return o.Object["status"] == nil
						}
					}
				}
			}
			desc = gvkDescriptor{reconciliationCheck: rc}
		}

		desc.gvks = append(desc.gvks, gvks...)
		ts.monitoredGvks[typ] = desc

		return nil
	}

	for _, obj := range ts.ToCreate {
		if err := addToMonitoredGvks(obj); err != nil {
			return err
		}
	}

	for _, obj := range ts.MonitoredObjectTypes {
		if err := addToMonitoredGvks(obj); err != nil {
			return err
		}
	}

	return nil
}

// Find finds an object with given ObjectKey in the provided objects list.
func Find[T client.Object](objects *Objects, key client.ObjectKey) (ret T, found bool) {
	for i := range objects.objects {
		o := objects.objects[i]
		_, ok := o.(T)
		if !ok {
			continue
		}
		k := client.ObjectKeyFromObject(o)
		if k == key {
			ret = o.(T)
			found = true
			break
		}
	}

	return
}

// FindByNamePrefix finds all the objects in the supplied list that have names starting with
// the name of the provided key and are present in the namespace specified by the key.
func FindByNamePrefix[T client.Object](objects *Objects, key client.ObjectKey) (ret []T) {
	for i := range objects.objects {
		o := objects.objects[i]
		_, ok := o.(T)
		if !ok {
			continue
		}
		k := client.ObjectKeyFromObject(o)
		if k.Namespace == key.Namespace && strings.HasPrefix(k.Name, key.Name) {
			ret = append(ret, o.(T))
		}
	}

	return
}

// GetAll finds all the objects of given type in the supplied objects list.
func GetAll[T client.Object](objects *Objects) []T {
	ret := []T{}
	for i := range objects.objects {
		o := objects.objects[i]
		obj, ok := o.(T)
		if !ok {
			continue
		}
		ret = append(ret, obj)
	}

	return ret
}

// First returns the first object of given type in the provided objects list or nil if there are no objects
// of the type.
func First[T client.Object](objects *Objects) *T {
	for i := range objects.objects {
		o := objects.objects[i]
		obj, ok := o.(T)
		if !ok {
			continue
		}
		return &obj
	}

	return nil
}

// TriggerReconciliation updates the provided object with a "random-annon-to-trigger-reconcile" annotation (with
// a random value) so that a new reconciliation is performed.
func (ts *TestSetup) TriggerReconciliation(ctx context.Context, object client.Object) {
	ts.triggerReconciliation(ctx, Default, object)
}

func (ts *TestSetup) triggerReconciliation(ctx context.Context, g Gomega, object client.Object) {
	lg := ts.lg(ctx)
	gvks, _, err := ts.client.Scheme().ObjectKinds(object)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(gvks).NotTo(BeEmpty())

	var triggerFn func(client.Object)
	for _, gvk := range gvks {
		gk := gvk.GroupKind()
		if fn, ok := ts.ReconciliationTrigger[gk]; ok {
			triggerFn = fn
			break
		}
	}

	g.Eventually(func(gg Gomega) {
		// trigger the update of the token to force the reconciliation
		cpy := object.DeepCopyObject().(client.Object)
		err := ts.client.Get(ctx, client.ObjectKeyFromObject(object), cpy)
		if errors.IsNotFound(err) {
			// oh, well, it's gone, no way of reconciling it...
			lg.Info("wanted to force reconciliation but object is gone",
				"object", client.ObjectKeyFromObject(object),
				"kind", object.GetObjectKind().GroupVersionKind().String())
			return
		}
		lg.Info("forcing reconciliation",
			"object", client.ObjectKeyFromObject(object),
			"kind", object.GetObjectKind().GroupVersionKind().String())

		gg.Expect(err).NotTo(HaveOccurred())

		if triggerFn != nil {
			lg.Info("found a custom trigger function, calling it")
			triggerFn(cpy)
		} else {
			lg.Info("modifying an annotation in hopes of it triggering the reconciliation")
			annos := object.GetAnnotations()
			if annos == nil {
				annos = map[string]string{}
			}
			annos["random-anno-to-trigger-reconcile"] = string(uuid.NewUUID())
			cpy.SetAnnotations(annos)
		}

		gg.Expect(ts.client.Update(ctx, cpy)).To(Succeed())
	}).Should(Succeed())

	lg.Info("update to force reconciliation succeeded",
		"object", client.ObjectKeyFromObject(object),
		"kind", object.GetObjectKind().GroupVersionKind().String())
}

// BeforeEach is where the magic happens. It first checks that the cluster is empty, then calls the external state handler to
// capture its state, resets it, creates the required objects, and waits for the cluster state to settle (i.e. wait for
// the controllers to create all the additional objects and finish all the reconciles). Once this method returns,
// the TestSetup.InCluster contains the objects of interest as they exist in the cluster after all the reconciliations have been
// performed at least once after the external state has been reset.
//
// The `postCondition` is a (potentially `nil`) check that needs to succeed before we can claim the cluster reached the
// desired state. If it is `nil`, then only the best effort is made to wait for the controllers to finish
// the reconciliation (basically the only thing guaranteed is that the objects will have a status, i.e.
// the reconciliation happened at least once).
func (ts *TestSetup) BeforeEach(ctx context.Context, cl client.Client, postCondition func(Gomega)) {
	start := time.Now()

	ts.client = cl

	// Test that the basic preconditions are met before we even try to start preparing the tests...
	Expect(ts.ensureMonitoredGvks()).To(Succeed())
	Expect(ts.monitoredGvks).NotTo(BeEmpty(), "no monitored object types found. Please add some objects to create or explict monitored object types")

	// I've seen some timing issues where beforeeach seems to be executed in parallel with aftereach of the test before
	// which would cause problems because we assume that the tests run sequentially. Let's just wait here a little, to
	// try and clear that condition up.
	Eventually(func(g Gomega) {
		ts.validateClusterEmpty(ctx, g)
	}).Should(Succeed())

	if ts.ExternalStateHandler != nil {
		ts.ExternalStateHandler.Capture()
		ts.ExternalStateHandler.Reset()
	}

	if ts.Behavior.BeforeObjectsCreated != nil {
		ts.Behavior.BeforeObjectsCreated()
	}

	ts.InCluster = Objects{
		objects: make([]client.Object, 0, len(ts.ToCreate)),
	}

	for i := range ts.ToCreate {
		obj := ts.ToCreate[i].DeepCopyObject().(client.Object)
		Expect(ts.client.Create(ctx, obj)).To(Succeed())
		ts.InCluster.objects = append(ts.InCluster.objects, obj)
	}

	// we don't need to force reconcile here, because we just created the objects so a reconciliation is running..

	if ts.Behavior.AfterObjectsCreated != nil {
		if ts.Behavior.DontTriggerReconcileAfterObjectsCreated {
			ts.settleWithCluster(ctx, false, postCondition)
			ts.Behavior.AfterObjectsCreated(&ts.InCluster)
		} else {
			ts.settleWithCluster(ctx, false, nil)
			ts.Behavior.AfterObjectsCreated(&ts.InCluster)
			ts.settleWithCluster(ctx, true, postCondition)
		}
	} else {
		ts.settleWithCluster(ctx, false, postCondition)
	}

	lg := ts.lg(ctx)

	lg.Info("=====")
	lg.Info("=====")
	lg.Info("=====")
	lg.Info("=====")
	lg.Info("===== All objects created and cluster state settled")
	lg.Info("===== For test: " + ginkgo.CurrentGinkgoTestDescription().FullTestText)
	lg.Info(fmt.Sprintf("===== Setup complete in %dms", time.Since(start).Milliseconds()))
	lg.Info("=====")
	lg.Info("=====")
	lg.Info("=====")
	lg.Info("=====")
}

// AfterEach cleans up all the objects from the cluster and reverts the external state to what it was before the test
// started (to what BeforeEach stored).
func (ts *TestSetup) AfterEach(ctx context.Context) {
	Expect(ts.ensureMonitoredGvks()).To(Succeed())
	for _, desc := range ts.monitoredGvks {
		for _, gvk := range desc.gvks {
			list := unstructured.UnstructuredList{}
			list.SetGroupVersionKind(gvk)
			Expect(ts.client.List(ctx, &list)).To(Succeed())
			for _, o := range list.Items {
				Expect(ts.client.Delete(ctx, &o)).To(Or(Succeed(), WithTransform(errors.IsNotFound, BeTrue())))
			}
		}
	}

	ts.InCluster.objects = []client.Object{}

	if ts.ExternalStateHandler != nil {
		ts.ExternalStateHandler.Restore()
	}

	Eventually(func(g Gomega) {
		ts.validateClusterEmpty(ctx, g)
	}).Should(Succeed())
}

func (ts *TestSetup) validateClusterEmpty(ctx context.Context, g Gomega) {
	for _, desc := range ts.monitoredGvks {
		for _, gvk := range desc.gvks {
			list := unstructured.UnstructuredList{}
			list.SetGroupVersionKind(gvk)
			g.Expect(ts.client.List(ctx, &list)).To(Succeed())
			g.Expect(list.Items).To(BeEmpty())
		}
	}
}

// ReconcileWithCluster triggers the reconciliation and waits for the cluster to settle again.
// The cluster is considered settled when there are no changes of the objects between
// consecutive readings of the objects (this happens for all monitored object types).
//
// The cluster is considered settled when there are no changes of the objects between
// consecutive readings of the objects (this happens for all monitored object types).
//
// The `postCondition` is a (potentially `nil`) check that needs to succeed before we can claim the cluster reached the
// desired state. If it is `nil`, then only the best effort is made to wait for the controllers to finish
// the reconciliation.
//
// The `postCondition` can use the `testSetup.InCluster` to access the current state of the objects (which is being
// updated during this call).
func (ts *TestSetup) ReconcileWithCluster(ctx context.Context, postCondition func(Gomega)) {
	lg := ts.lg(ctx)

	_, filename, line, _ := runtime.Caller(1)
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")
	lg.Info("//// Triggering reconciliation with the cluster")
	lg.Info("////", "file", filename, "line", line)
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")

	ts.settleWithCluster(ctx, true, postCondition)

	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\ Finished reconciliation with the cluster")
	lg.Info("\\\\\\\\", "file", filename, "line", line)
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
}

// SettleWithCluster doesn't trigger the reconciliation (like ReconcileWithCluster does) but merely waits for the cluster to settle.
// The cluster is considered settled when there are no changes of the objects between
// consecutive readings of the objects (this happens for all monitored object types).
//
// The `postCondition` is a (potentially `nil`) check that needs to succeed before we can claim the cluster reached the
// desired state. If it is `nil`, then only the best effort is made to wait for the controllers to finish
// the reconciliation.
//
// The `postCondition` can use the `testSetup.InCluster` to access the current state of the objects (which is being
// updated during this call).
func (ts *TestSetup) SettleWithCluster(ctx context.Context, postCondition func(Gomega)) {
	lg := ts.lg(ctx)

	_, filename, line, _ := runtime.Caller(1)
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")
	lg.Info("//// Settling with the cluster")
	lg.Info("////", "file", filename, "line", line)
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")
	lg.Info("////")

	ts.settleWithCluster(ctx, false, postCondition)

	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\ Finished settling with the cluster")
	lg.Info("\\\\\\\\", "file", filename, "line", line)
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
	lg.Info("\\\\\\\\")
}

func (ts *TestSetup) settleWithCluster(ctx context.Context, forceReconcile bool, postCondition func(Gomega)) {
	Expect(ts.ensureMonitoredGvks()).To(Succeed())

	waitForStatus := func() {
		for _, desc := range ts.monitoredGvks {
			for _, gvk := range desc.gvks {
				ts.waitForStatus(ctx, ts.client, &desc, gvk)
			}
		}
	}

	loadAll := func() {
		ts.InCluster.objects = []client.Object{}
		for _, desc := range ts.monitoredGvks {
			for _, gvk := range desc.gvks {
				ts.loadAll(ctx, gvk)
			}
		}
	}

	// we need to create copies of the objects from the cluster
	var lastClusterState []client.Object

	rememberCurrentClusterState := func() {
		lastClusterState = []client.Object{}
		for _, o := range ts.InCluster.objects {
			lastClusterState = append(lastClusterState, o.DeepCopyObject().(client.Object))
		}
	}

	lg := ts.lg(ctx)
	i := 0
	var lastReconcileTime *time.Time
	Eventually(func(g Gomega) {
		i += 1

		rememberCurrentClusterState()

		shouldReportProgress := lastReconcileTime != nil && time.Since(*lastReconcileTime) > 2*time.Second

		// this loop is usually very fast, so we trigger the reconciliation the first time and then only every 2s to
		// give the controllers some time to react.
		if lastReconcileTime == nil || shouldReportProgress {
			if shouldReportProgress {
				lg.Info("////")
				lg.Info("////")
				lg.Info("////")
				lg.Info("////")
				if forceReconcile {
					lg.Info("//// Cluster state still in progress after (another) 2s. Triggering reconciliation again.")
				} else {
					lg.Info("//// Cluster state still in progress after (another) 2s. Postcondition has not passed yet.")
				}
				lg.Info("////")
				lg.Info("////")
				lg.Info("////")
				lg.Info("////")
			}

			if forceReconcile {
				for _, o := range ts.InCluster.objects {
					ts.triggerReconciliation(ctx, g, o)
				}
			}
			now := time.Now()
			lastReconcileTime = &now
		}

		waitForStatus()
		loadAll()

		// ok, so now we're in one of 2 possible states wrt reconciliation:
		// 1) the reconciliation happened for the first time (the objects didn't have status, and we waited for
		//    the controllers to fill it in,
		// 2) the objects have already been reconciled before and we either forced reconciliation or not. We don't know
		//    if any changes are yet to happen in the cluster as controllers react or if the reconciliation already
		//    finished.
		//
		// Therefore, we can do just 2 things: we can monitor if any change has already happened in the cluster and
		// check that the post condition is passing. Neither of those things actually guarantees that the reconciliation
		// will have happened by the time we return from this method. But that's OK. If the caller wanted to trigger
		// reconciliation, they most probably also have provided a post condition to check the desired changes have
		// actually happened. If the caller didn't provide a post condition, they have been warned - we only offer
		// waiting for reconciliation on the best effort basis.

		diffs := findDifferences(lastClusterState, ts.InCluster.objects)

		if len(diffs) > 0 {
			if i > 1 {
				lg.Info("~~~~")
				lg.Info("~~~~")
				lg.Info("~~~~")
				lg.Info("~~~~")
				lg.Info("~~~~ settling loop still seeing changes",
					"iteration", i,
					"test", ginkgo.CurrentGinkgoTestDescription().FullTestText,
					"diffs", diffs,
				)
				lg.Info("~~~~")
				lg.Info("~~~~")
				lg.Info("~~~~")
				lg.Info("~~~~")
			}

			time.Sleep(200 * time.Millisecond)

			// true here is the result of the if statement that we're nested in... We expect that to be false :)
			g.Expect(true).To(BeFalse())
			// the cluster state is still evolving, no need to bother with calling postCondition yet
			return
		} else if shouldReportProgress {
			lg.Info("~~~~")
			lg.Info("~~~~")
			lg.Info("~~~~")
			lg.Info("~~~~")
			lg.Info("~~~~ No change in cluster state detected.")
			lg.Info("~~~~")
			lg.Info("~~~~")
			lg.Info("~~~~")
			lg.Info("~~~~")
		}

		if postCondition != nil {
			postCondition(g)
		}

		i += 1
	}).Should(Succeed())
}

func findDifferences(origs []client.Object, news []client.Object) string {
	origsByType := splitByType(origs)
	newsByType := splitByType(news)

	ret := ""

	for t, os := range origsByType {
		ns := newsByType[t]
		d := diffArray(os, ns)
		if d != "" {
			if ret != "" {
				ret += ", "
			}
			ret += fmt.Sprintf("objects of type %s: %s", t.Elem().Name(), d)
		}
	}

	return ret
}

func diffArray(origs []client.Object, news []client.Object) string {
	if len(origs) != len(news) {
		return "arrays have a different number of elements"
	}

	var diffs []string

	origMap := map[client.ObjectKey]client.Object{}
	for _, o := range origs {
		origMap[client.ObjectKeyFromObject(o)] = o
	}

	newMap := map[client.ObjectKey]client.Object{}
	for _, o := range news {
		newMap[client.ObjectKeyFromObject(o)] = o
	}

	for k := range origMap {
		if _, ok := newMap[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("%v: {not found in the new set}", k))
			delete(origMap, k)
		}
	}

	for k := range newMap {
		if _, ok := origMap[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("%v: {not found in the old set}", k))
			delete(newMap, k)
		}
	}

	for key, n := range newMap {
		o := origMap[key]
		diff := diff(o, n)

		if len(diff) > 0 {
			diffs = append(diffs, fmt.Sprintf("%v: {%s}", key, diff))
		}
	}

	return strings.Join(diffs, ", ")
}

func splitByType(objs []client.Object) map[reflect.Type][]client.Object {
	ret := map[reflect.Type][]client.Object{}
	for i := range objs {
		obj := objs[i]
		typ := reflect.TypeOf(obj)
		objsOfType := ret[typ]
		objsOfType = append(objsOfType, obj)
		ret[typ] = objsOfType
	}
	return ret
}

func diff(a client.Object, b client.Object) string {
	copyA := a.DeepCopyObject().(client.Object)
	copyB := b.DeepCopyObject().(client.Object)

	// remove the metadata from the objects
	copyA.SetAnnotations(nil)
	copyA.SetResourceVersion("")
	copyA.SetManagedFields(nil)
	copyB.SetAnnotations(nil)
	copyB.SetResourceVersion("")
	copyB.SetManagedFields(nil)

	return strings.Join(deep.Equal(copyA, copyB), ", ")
}

func (ts *TestSetup) loadAll(ctx context.Context, gvk schema.GroupVersionKind) {
	list := unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)

	Expect(ts.client.List(ctx, &list)).To(Succeed())

	for _, uo := range list.Items {
		data, err := uo.MarshalJSON()
		Expect(err).NotTo(HaveOccurred(), "failed to marshal object of kind %s to json", gvk)
		o, err := ts.client.Scheme().New(gvk)
		Expect(err).NotTo(HaveOccurred(), "failed to create a new instance of object with gvk %s", gvk)
		Expect(json.Unmarshal(data, o)).To(Succeed(), "failed to unmarshal unstructured data to an object of kind %s", gvk)
		ts.InCluster.objects = append(ts.InCluster.objects, o.(client.Object))
	}
}

func (ts *TestSetup) waitForStatus(ctx context.Context, cl client.Client, desc *gvkDescriptor, gvk schema.GroupVersionKind) {
	if desc.reconciliationCheck == nil {
		return
	}

	Eventually(func(g Gomega) {
		list := unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)

		g.Expect(cl.List(ctx, &list)).To(Succeed())

		for _, o := range list.Items {
			shouldReconcile := desc.reconciliationCheck(&o)
			g.Expect(shouldReconcile).To(BeFalse(),
				"object %s of type %s was determined need reconciliation",
				client.ObjectKey{Name: o.GetName(), Namespace: o.GetNamespace()},
				gvk)
		}
	}).Should(Succeed(), "failed to wait for reconciliation status of objects of type %s", gvk)
}

func (ts *TestSetup) lg(ctx context.Context) logr.Logger {
	return log.FromContext(ctx).V(ts.LogLevel)
}
