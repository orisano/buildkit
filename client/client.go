package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"time"

	"github.com/grpc-ecosystem/grpc-opentracing/go/otgrpc"
	controlapi "github.com/moby/buildkit/api/services/control"
	"github.com/moby/buildkit/util/appdefaults"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type Client struct {
	conn *grpc.ClientConn
}

type ClientOpt interface{}

// New returns a new buildkit client. Address can be empty for the system-default address.
func New(address string, opts ...ClientOpt) (*Client, error) {
	gopts := []grpc.DialOption{
		grpc.WithDialer(dialer),
		grpc.FailOnNonTempDialError(true),
	}

	timeout := 30 * time.Second
	needWithInsecure := true

	for _, o := range opts {
		switch opt := o.(type) {
		case *withBlockOpt:
			gopts = append(gopts, grpc.WithBlock(), grpc.FailOnNonTempDialError(true))
		case *withCredentials:
			gopt, err := loadCredentials(opt)
			if err != nil {
				return nil, err
			}
			gopts = append(gopts, gopt)
			needWithInsecure = false
		case *withTracer:
			gopts = append(gopts,
				grpc.WithUnaryInterceptor(otgrpc.OpenTracingClientInterceptor(opt.tracer, otgrpc.LogPayloads())),
				grpc.WithStreamInterceptor(otgrpc.OpenTracingStreamClientInterceptor(opt.tracer)))
		case *withTimeout:
			timeout = opt.duration
		default:
		}
	}

	if needWithInsecure {
		gopts = append(gopts, grpc.WithInsecure())
	}

	if address == "" {
		address = appdefaults.Address
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, address, gopts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q . make sure buildkitd is running", address)
	}
	c := &Client{
		conn: conn,
	}
	return c, nil
}

func (c *Client) controlClient() controlapi.ControlClient {
	return controlapi.NewControlClient(c.conn)
}

func (c *Client) Close() error {
	return c.conn.Close()
}

type withBlockOpt struct{}

func WithBlock() ClientOpt {
	return &withBlockOpt{}
}

type withCredentials struct {
	ServerName string
	CACert     string
	Cert       string
	Key        string
}

// WithCredentials configures the TLS parameters of the client.
// Arguments:
// * serverName: specifies the name of the target server
// * ca:				 specifies the filepath of the CA certificate to use for verification
// * cert:			 specifies the filepath of the client certificate
// * key:				 specifies the filepath of the client key
func WithCredentials(serverName, ca, cert, key string) ClientOpt {
	return &withCredentials{serverName, ca, cert, key}
}

func loadCredentials(opts *withCredentials) (grpc.DialOption, error) {
	ca, err := ioutil.ReadFile(opts.CACert)
	if err != nil {
		return nil, errors.Wrap(err, "could not read ca certificate")
	}

	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(ca); !ok {
		return nil, errors.New("failed to append ca certs")
	}

	cfg := &tls.Config{
		ServerName: opts.ServerName,
		RootCAs:    certPool,
	}

	// we will produce an error if the user forgot about either cert or key if at least one is specified
	if opts.Cert != "" || opts.Key != "" {
		cert, err := tls.LoadX509KeyPair(opts.Cert, opts.Key)
		if err != nil {
			return nil, errors.Wrap(err, "could not read certificate/key")
		}
		cfg.Certificates = []tls.Certificate{cert}
		cfg.BuildNameToCertificate()
	}

	return grpc.WithTransportCredentials(credentials.NewTLS(cfg)), nil
}

func WithTracer(t opentracing.Tracer) ClientOpt {
	return &withTracer{t}
}

type withTracer struct {
	tracer opentracing.Tracer
}

type withTimeout struct {
	duration time.Duration
}

func WithTimeout(d time.Duration) ClientOpt {
	return &withTimeout{d}
}
