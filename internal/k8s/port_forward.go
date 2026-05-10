package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwardOptions captures the flag-parsed inputs to
// `roksbnkctl k port-forward`.
//
// Ports is the same slice form as kubectl ("8080:80", "5000",
// "5000:5000"). StopCh closes the tunnel; cancelling the context also
// closes it. ReadyCh receives a single empty struct when the tunnel
// is wired up and accepting connections; useful for tests / orchestration.
type PortForwardOptions struct {
	PodName        string
	Namespace      string
	Ports          []string
	KubeconfigPath string

	IOStreams genericiooptions.IOStreams

	// StopCh + ReadyCh: optional. If StopCh is nil, the helper
	// allocates one and closes it on context cancel.
	StopCh  chan struct{}
	ReadyCh chan struct{}
}

// Run opens a port-forward tunnel against the pod and blocks until
// either the context is cancelled or the tunnel errors out.
func (o *PortForwardOptions) Run(ctx context.Context) error {
	if o.PodName == "" {
		return errors.New("pod name required")
	}
	if len(o.Ports) == 0 {
		return errors.New("at least one <local>:<remote> port mapping required")
	}
	if o.IOStreams.Out == nil {
		o.IOStreams.Out = io.Discard
	}
	if o.IOStreams.ErrOut == nil {
		o.IOStreams.ErrOut = io.Discard
	}

	cfg, err := BuildRESTConfig(o.KubeconfigPath)
	if err != nil {
		return err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	ns := o.Namespace
	if ns == "" {
		ns = "default"
	}

	roundTripper, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return fmt.Errorf("setting up SPDY round-tripper: %w", err)
	}

	req := cs.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(o.PodName).
		Namespace(ns).
		SubResource("portforward")

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, "POST", req.URL())

	stopCh := o.StopCh
	if stopCh == nil {
		stopCh = make(chan struct{})
	}
	readyCh := o.ReadyCh
	if readyCh == nil {
		readyCh = make(chan struct{})
	}

	// Cancel on ctx done — the cobra root wires SIGINT into ctx so
	// Ctrl+C closes the tunnel cleanly.
	go func() {
		<-ctx.Done()
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
	}()

	fwd, err := portforward.New(dialer, o.Ports, stopCh, readyCh, o.IOStreams.Out, o.IOStreams.ErrOut)
	if err != nil {
		return fmt.Errorf("creating port forwarder: %w", err)
	}
	return fwd.ForwardPorts()
}
