// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"errors"
	"fmt"
	"time"

	"github.com/tsuru/tsuru/provision"
	"github.com/tsuru/tsuru/provision/provisiontest"
	"gopkg.in/check.v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/pkg/api/v1"
	extensions "k8s.io/client-go/pkg/apis/extensions/v1beta1"
	ktesting "k8s.io/client-go/testing"
)

func (s *S) TestDeploymentNameForApp(c *check.C) {
	var tests = []struct {
		name, process, expected string
	}{
		{"myapp", "p1", "myapp-p1"},
		{"MYAPP", "p-1", "myapp-p-1"},
		{"my-app_app", "P_1-1", "my-app-app-p-1-1"},
	}
	for i, tt := range tests {
		a := provisiontest.NewFakeApp(tt.name, "plat", 1)
		c.Assert(deploymentNameForApp(a, tt.process), check.Equals, tt.expected, check.Commentf("test %d", i))
	}
}

func (s *S) TestDeployPodNameForApp(c *check.C) {
	var tests = []struct {
		name, expected string
	}{
		{"myapp", "myapp-deploy"},
		{"MYAPP", "myapp-deploy"},
		{"my-app_app", "my-app-app-deploy"},
	}
	for i, tt := range tests {
		a := provisiontest.NewFakeApp(tt.name, "plat", 1)
		c.Assert(deployPodNameForApp(a), check.Equals, tt.expected, check.Commentf("test %d", i))
	}
}

func (s *S) TestExecCommandPodNameForApp(c *check.C) {
	var tests = []struct {
		name, expected string
	}{
		{"myapp", "myapp-isolated-run"},
		{"MYAPP", "myapp-isolated-run"},
		{"my-app_app", "my-app-app-isolated-run"},
	}
	for i, tt := range tests {
		a := provisiontest.NewFakeApp(tt.name, "plat", 1)
		c.Assert(execCommandPodNameForApp(a), check.Equals, tt.expected, check.Commentf("test %d", i))
	}
}

func (s *S) TestDaemonSetName(c *check.C) {
	var tests = []struct {
		name, pool, expected string
	}{
		{"d1", "", "node-container-d1-all"},
		{"D1", "", "node-container-d1-all"},
		{"d1_x", "", "node-container-d1-x-all"},
		{"d1", "p1", "node-container-d1-pool-p1"},
		{"d1", "P1", "node-container-d1-pool-p1"},
		{"d1", "P_1", "node-container-d1-pool-p-1"},
		{"d1", "P-x_1", "node-container-d1-pool-p-x-1"},
	}
	for i, tt := range tests {
		c.Assert(daemonSetName(tt.name, tt.pool), check.Equals, tt.expected, check.Commentf("test %d", i))
	}
}

func (s *S) TestWaitFor(c *check.C) {
	err := waitFor(100*time.Millisecond, func() (bool, error) {
		return true, nil
	})
	c.Assert(err, check.IsNil)
	err = waitFor(100*time.Millisecond, func() (bool, error) {
		return false, nil
	})
	c.Assert(err, check.ErrorMatches, `timeout after .*`)
	err = waitFor(100*time.Millisecond, func() (bool, error) {
		return true, errors.New("myerr")
	})
	c.Assert(err, check.ErrorMatches, `myerr`)
}

func (s *S) TestWaitForPodContainersRunning(c *check.C) {
	err := waitForPodContainersRunning(s.client.clusterClient, "pod1", 100*time.Millisecond)
	c.Assert(err, check.ErrorMatches, `Pod "pod1" not found`)
	var wantedPhase v1.PodPhase
	var wantedStates []v1.ContainerState
	s.client.PrependReactor("create", "pods", func(action ktesting.Action) (handled bool, ret runtime.Object, err error) {
		pod, ok := action.(ktesting.CreateAction).GetObject().(*v1.Pod)
		c.Assert(ok, check.Equals, true)
		pod.Status.Phase = wantedPhase
		statuses := make([]v1.ContainerStatus, len(wantedStates))
		for i, s := range wantedStates {
			statuses[i] = v1.ContainerStatus{Name: fmt.Sprintf("c-%d", i), State: s}
		}
		pod.Status.ContainerStatuses = statuses
		return false, nil, nil
	})
	tests := []struct {
		states []v1.ContainerState
		phase  v1.PodPhase
		err    string
	}{
		{phase: v1.PodSucceeded},
		{phase: v1.PodPending, err: `timeout after .*`},
		{phase: v1.PodFailed, err: `invalid pod phase "Failed"`},
		{phase: v1.PodUnknown, err: `invalid pod phase "Unknown"`},
		{phase: v1.PodRunning, states: []v1.ContainerState{
			{},
		}, err: `timeout after .*`},
		{phase: v1.PodRunning, states: []v1.ContainerState{
			{Running: &v1.ContainerStateRunning{}}, {},
		}, err: `timeout after .*`},
		{phase: v1.PodRunning, states: []v1.ContainerState{
			{Running: &v1.ContainerStateRunning{}}, {Running: &v1.ContainerStateRunning{}},
		}},
		{phase: v1.PodRunning, states: []v1.ContainerState{
			{Running: &v1.ContainerStateRunning{}}, {Terminated: &v1.ContainerStateTerminated{
				ExitCode: 9, Reason: "x", Message: "y",
			}},
		}, err: `unexpected container "c-1" termination: Exit 9 - Reason: "x" - Message: "y"`},
	}
	for _, tt := range tests {
		wantedPhase = tt.phase
		wantedStates = tt.states
		_, err = s.client.Core().Pods(s.client.Namespace()).Create(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod1",
				Namespace: s.client.Namespace(),
			},
		})
		c.Assert(err, check.IsNil)
		err = waitForPodContainersRunning(s.client.clusterClient, "pod1", 100*time.Millisecond)
		if tt.err == "" {
			c.Assert(err, check.IsNil)
		} else {
			c.Assert(err, check.ErrorMatches, tt.err)
		}
		err = cleanupPod(s.client.clusterClient, "pod1")
		c.Assert(err, check.IsNil)
	}
}

func (s *S) TestWaitForPod(c *check.C) {
	err := waitForPod(s.client.clusterClient, "pod1", false, 100*time.Millisecond)
	c.Assert(err, check.ErrorMatches, `Pod "pod1" not found`)
	var wantedPhase v1.PodPhase
	var wantedMessage string
	s.client.PrependReactor("create", "pods", func(action ktesting.Action) (handled bool, ret runtime.Object, err error) {
		pod, ok := action.(ktesting.CreateAction).GetObject().(*v1.Pod)
		c.Assert(ok, check.Equals, true)
		pod.Status.Phase = wantedPhase
		pod.Status.Message = wantedMessage
		return false, nil, nil
	})
	tests := []struct {
		phase   v1.PodPhase
		msg     string
		err     string
		evt     *v1.Event
		running bool
	}{
		{phase: v1.PodSucceeded},
		{phase: v1.PodRunning, err: `timeout after .*`},
		{phase: v1.PodRunning, running: true},
		{phase: v1.PodPending, err: `timeout after .*`},
		{phase: v1.PodFailed, err: `invalid pod phase "Failed"`},
		{phase: v1.PodFailed, msg: "my error msg", err: `invalid pod phase "Failed"\("my error msg"\)`},
		{phase: v1.PodUnknown, err: `invalid pod phase "Unknown"`},
		{phase: v1.PodFailed, err: `invalid pod phase "Failed": my evt message`, evt: &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod1.evt1",
				Namespace: s.client.Namespace(),
			},
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "pod1",
				Namespace: s.client.Namespace(),
			},
			Message: "my evt message",
		}},
	}
	for _, tt := range tests {
		wantedPhase = tt.phase
		wantedMessage = tt.msg
		_, err = s.client.Core().Pods(s.client.Namespace()).Create(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod1",
				Namespace: s.client.Namespace(),
			},
		})
		c.Assert(err, check.IsNil)
		if tt.evt != nil {
			_, err = s.client.Core().Events(s.client.Namespace()).Create(tt.evt)
			c.Assert(err, check.IsNil)
		}
		err = waitForPod(s.client.clusterClient, "pod1", tt.running, 100*time.Millisecond)
		if tt.err == "" {
			c.Assert(err, check.IsNil)
		} else {
			c.Assert(err, check.ErrorMatches, tt.err)
		}
		err = cleanupPod(s.client.clusterClient, "pod1")
		c.Assert(err, check.IsNil)
	}
}

func (s *S) TestCleanupPods(c *check.C) {
	for i := 0; i < 3; i++ {
		labels := map[string]string{"a": "x"}
		if i == 2 {
			labels["a"] = "y"
		}
		_, err := s.client.Core().Pods(s.client.Namespace()).Create(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("pod-%d", i),
				Namespace: s.client.Namespace(),
				Labels:    labels,
			},
		})
		c.Assert(err, check.IsNil)
	}
	err := cleanupPods(s.client.clusterClient, metav1.ListOptions{
		LabelSelector: "a=x",
	})
	c.Assert(err, check.IsNil)
	pods, err := s.client.Core().Pods(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(pods.Items, check.DeepEquals, []v1.Pod{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: s.client.Namespace(),
			Labels:    map[string]string{"a": "y"},
		},
	}})
}

func (s *S) TestCleanupDeployment(c *check.C) {
	a := provisiontest.NewFakeApp("myapp", "plat", 1)
	expectedLabels := map[string]string{
		"tsuru.io/is-tsuru":             "true",
		"tsuru.io/is-service":           "true",
		"tsuru.io/is-build":             "false",
		"tsuru.io/is-stopped":           "false",
		"tsuru.io/is-deploy":            "false",
		"tsuru.io/is-isolated-run":      "false",
		"tsuru.io/restarts":             "0",
		"tsuru.io/app-name":             "myapp",
		"tsuru.io/app-process":          "p1",
		"tsuru.io/app-process-replicas": "1",
		"tsuru.io/app-platform":         "plat",
		"tsuru.io/app-pool":             "test-default",
		"tsuru.io/router-type":          "fake",
		"tsuru.io/router-name":          "fake",
		"tsuru.io/provisioner":          "kubernetes",
	}
	_, err := s.client.Extensions().Deployments(s.client.Namespace()).Create(&extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-p1",
			Namespace: s.client.Namespace(),
		},
	})
	c.Assert(err, check.IsNil)
	_, err = s.client.Extensions().ReplicaSets(s.client.Namespace()).Create(&extensions.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-p1-xxx",
			Namespace: s.client.Namespace(),
			Labels:    expectedLabels,
		},
	})
	c.Assert(err, check.IsNil)
	_, err = s.client.Core().Pods(s.client.Namespace()).Create(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-p1-xyz",
			Namespace: s.client.Namespace(),
			Labels:    expectedLabels,
		},
	})
	c.Assert(err, check.IsNil)
	err = cleanupDeployment(s.client.clusterClient, a, "p1")
	c.Assert(err, check.IsNil)
	deps, err := s.client.Extensions().Deployments(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(deps.Items, check.HasLen, 0)
	pods, err := s.client.Core().Pods(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(pods.Items, check.HasLen, 0)
	replicas, err := s.client.Extensions().ReplicaSets(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(replicas.Items, check.HasLen, 0)
}

func (s *S) TestCleanupReplicas(c *check.C) {
	_, err := s.client.Extensions().ReplicaSets(s.client.Namespace()).Create(&extensions.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-p1-xxx",
			Namespace: s.client.Namespace(),
			Labels: map[string]string{
				"a": "x",
			},
		},
	})
	c.Assert(err, check.IsNil)
	_, err = s.client.Core().Pods(s.client.Namespace()).Create(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-p1-xyz",
			Namespace: s.client.Namespace(),
			Labels: map[string]string{
				"a": "x",
			},
		},
	})
	c.Assert(err, check.IsNil)
	err = cleanupReplicas(s.client.clusterClient, metav1.ListOptions{
		LabelSelector: "a=x",
	})
	c.Assert(err, check.IsNil)
	deps, err := s.client.Extensions().Deployments(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(deps.Items, check.HasLen, 0)
	pods, err := s.client.Core().Pods(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(pods.Items, check.HasLen, 0)
	replicas, err := s.client.Extensions().ReplicaSets(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(replicas.Items, check.HasLen, 0)
}

func (s *S) TestCleanupDaemonSet(c *check.C) {
	_, err := s.client.Extensions().DaemonSets(s.client.Namespace()).Create(&extensions.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-container-bs-pool-p1",
			Namespace: s.client.Namespace(),
		},
	})
	c.Assert(err, check.IsNil)
	_, err = s.client.Core().Pods(s.client.Namespace()).Create(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-container-bs-pool-p1-xyz",
			Namespace: s.client.Namespace(),
			Labels: map[string]string{
				"tsuru.io/is-tsuru":            "true",
				"tsuru.io/is-node-container":   "true",
				"tsuru.io/provisioner":         provisionerName,
				"tsuru.io/node-container-name": "bs",
				"tsuru.io/node-container-pool": "p1",
			},
		},
	})
	c.Assert(err, check.IsNil)
	err = cleanupDaemonSet(s.client.clusterClient, "bs", "p1")
	c.Assert(err, check.IsNil)
	daemons, err := s.client.Extensions().DaemonSets(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(daemons.Items, check.HasLen, 0)
	pods, err := s.client.Core().Pods(s.client.Namespace()).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	c.Assert(pods.Items, check.HasLen, 0)
}

func (s *S) TestLabelSetFromMeta(c *check.C) {
	meta := metav1.ObjectMeta{
		Labels: map[string]string{
			"tsuru.io/x": "a",
			"y":          "b",
		},
		Annotations: map[string]string{
			"tsuru.io/a": "1",
			"b":          "2",
		},
	}
	ls := labelSetFromMeta(&meta)
	c.Assert(ls, check.DeepEquals, &provision.LabelSet{
		Labels: map[string]string{
			"tsuru.io/x": "a",
			"y":          "b",
			"tsuru.io/a": "1",
			"b":          "2",
		},
		Prefix: tsuruLabelPrefix,
	})
}

func (s *S) TestGetServicePort(c *check.C) {
	port, err := getServicePort(s.client.clusterClient, "notfound")
	c.Assert(err, check.IsNil)
	c.Assert(port, check.Equals, int32(0))
	_, err = s.client.Core().Services(s.client.Namespace()).Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "srv1",
			Namespace: s.client.Namespace(),
		},
	})
	c.Assert(err, check.IsNil)
	port, err = getServicePort(s.client.clusterClient, "srv1")
	c.Assert(err, check.IsNil)
	c.Assert(port, check.Equals, int32(0))
	_, err = s.client.Core().Services(s.client.Namespace()).Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "srv2",
			Namespace: s.client.Namespace(),
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{NodePort: 123}},
		},
	})
	c.Assert(err, check.IsNil)
	port, err = getServicePort(s.client.clusterClient, "srv2")
	c.Assert(err, check.IsNil)
	c.Assert(port, check.Equals, int32(123))
}
