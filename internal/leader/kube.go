package leader

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ServiceAccountDir is where Kubernetes mounts a pod's service account token,
// CA certificate and namespace.
const ServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// AddressAnnotation holds the leader's address on the Lease.
const AddressAnnotation = "am-autotask.andrewheberle.github.io/address"

// microTime is the format of a Lease's acquireTime and renewTime.
const microTime = "2006-01-02T15:04:05.000000Z07:00"

// InCluster describes how to reach the Kubernetes API from inside a pod.
type InCluster struct {
	// Dir holds the service account files. Default [ServiceAccountDir].
	Dir string
	// Getenv reads KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT.
	// Default [os.Getenv].
	Getenv func(string) string
}

// Namespace returns the pod's namespace.
func (c InCluster) Namespace() (string, error) {
	b, err := os.ReadFile(filepath.Join(c.dir(), "namespace"))
	if err != nil {
		return "", fmt.Errorf("reading pod namespace: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func (c InCluster) dir() string {
	if c.Dir == "" {
		return ServiceAccountDir
	}
	return c.Dir
}

// Client returns a [Client] for the named Lease, failing unless running in
// a pod with a service account.
func (c InCluster) Client(namespace, name string) (Client, error) {
	getenv := c.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	host, port := getenv("KUBERNETES_SERVICE_HOST"), getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("not running in Kubernetes: KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT are not set")
	}

	tokenPath := filepath.Join(c.dir(), "token")
	if _, err := os.Stat(tokenPath); err != nil {
		return nil, fmt.Errorf("reading service account token: %w", err)
	}
	ca, err := os.ReadFile(filepath.Join(c.dir(), "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("reading Kubernetes CA certificate: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("the Kubernetes CA certificate file contains no certificates")
	}

	base := "https://" + net.JoinHostPort(host, port)
	return NewKubeClient(base, namespace, name, tokenPath, &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}), nil
}

// KubeClient is a [Client] using the Kubernetes coordination.k8s.io/v1 API.
type KubeClient struct {
	url       string
	namespace string
	name      string
	tokenPath string
	http      *http.Client
}

// NewKubeClient returns a client for the Lease name in namespace on the API
// server at base, authenticating with the token read from tokenPath before
// each request, as Kubernetes rotates it.
func NewKubeClient(base, namespace, name, tokenPath string, hc *http.Client) *KubeClient {
	return &KubeClient{
		url:       strings.TrimSuffix(base, "/") + "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(namespace) + "/leases",
		namespace: namespace,
		name:      name,
		tokenPath: tokenPath,
		http:      hc,
	}
}

type kubeLease struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   kubeMeta      `json:"metadata"`
	Spec       kubeLeaseSpec `json:"spec"`
}

type kubeMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

type kubeLeaseSpec struct {
	HolderIdentity       *string `json:"holderIdentity,omitempty"`
	LeaseDurationSeconds *int32  `json:"leaseDurationSeconds,omitempty"`
	AcquireTime          *string `json:"acquireTime,omitempty"`
	RenewTime            *string `json:"renewTime,omitempty"`
	LeaseTransitions     *int32  `json:"leaseTransitions,omitempty"`
}

// Get implements [Client].
func (k *KubeClient) Get(ctx context.Context) (Lease, error) {
	return k.do(ctx, http.MethodGet, k.url+"/"+url.PathEscape(k.name), nil)
}

// Create implements [Client].
func (k *KubeClient) Create(ctx context.Context, l Lease) (Lease, error) {
	return k.do(ctx, http.MethodPost, k.url, k.encode(l))
}

// Update implements [Client].
func (k *KubeClient) Update(ctx context.Context, l Lease) (Lease, error) {
	return k.do(ctx, http.MethodPut, k.url+"/"+url.PathEscape(k.name), k.encode(l))
}

func (k *KubeClient) encode(l Lease) *kubeLease {
	seconds := int32(l.Duration / time.Second)
	transitions := l.Transitions
	kl := &kubeLease{
		APIVersion: "coordination.k8s.io/v1",
		Kind:       "Lease",
		Metadata: kubeMeta{
			Name:            k.name,
			Namespace:       k.namespace,
			ResourceVersion: l.ResourceVersion,
		},
		Spec: kubeLeaseSpec{
			HolderIdentity:       &l.Holder,
			LeaseDurationSeconds: &seconds,
			LeaseTransitions:     &transitions,
		},
	}
	if l.Address != "" {
		kl.Metadata.Annotations = map[string]string{AddressAnnotation: l.Address}
	}
	if !l.AcquireTime.IsZero() {
		s := l.AcquireTime.UTC().Format(microTime)
		kl.Spec.AcquireTime = &s
	}
	if !l.RenewTime.IsZero() {
		s := l.RenewTime.UTC().Format(microTime)
		kl.Spec.RenewTime = &s
	}
	return kl
}

func decode(kl kubeLease) (Lease, error) {
	l := Lease{
		Address:         kl.Metadata.Annotations[AddressAnnotation],
		ResourceVersion: kl.Metadata.ResourceVersion,
	}
	s := kl.Spec
	if s.HolderIdentity != nil {
		l.Holder = *s.HolderIdentity
	}
	if s.LeaseDurationSeconds != nil {
		l.Duration = time.Duration(*s.LeaseDurationSeconds) * time.Second
	}
	if s.LeaseTransitions != nil {
		l.Transitions = *s.LeaseTransitions
	}
	for _, t := range []struct {
		value *string
		into  *time.Time
	}{{s.AcquireTime, &l.AcquireTime}, {s.RenewTime, &l.RenewTime}} {
		if t.value == nil {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, *t.value)
		if err != nil {
			return Lease{}, fmt.Errorf("parsing lease time %q: %w", *t.value, err)
		}
		*t.into = parsed
	}
	return l, nil
}

func (k *KubeClient) do(ctx context.Context, method, u string, body *kubeLease) (Lease, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Lease{}, err
		}
		r = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return Lease{}, err
	}
	token, err := os.ReadFile(k.tokenPath)
	if err != nil {
		return Lease{}, fmt.Errorf("reading service account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := k.http.Do(req)
	if err != nil {
		return Lease{}, fmt.Errorf("%s lease: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Lease{}, fmt.Errorf("%s lease: reading response: %w", method, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Lease{}, ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		return Lease{}, ErrConflict
	case resp.StatusCode >= 300:
		return Lease{}, fmt.Errorf("%s lease: %s: %s", method, resp.Status, apiMessage(b))
	}

	var kl kubeLease
	if err := json.Unmarshal(b, &kl); err != nil {
		return Lease{}, fmt.Errorf("%s lease: decoding response: %w", method, err)
	}
	return decode(kl)
}

// apiMessage returns the message of a Kubernetes Status response, or the
// body as is.
func apiMessage(b []byte) string {
	var status struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &status) == nil && status.Message != "" {
		return status.Message
	}
	return strings.TrimSpace(string(b))
}
