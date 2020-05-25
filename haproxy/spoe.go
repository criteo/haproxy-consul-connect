package haproxy

import (
	"crypto/x509"
	"fmt"

	log "github.com/sirupsen/logrus"

	spoe "github.com/criteo/haproxy-spoe-go"
	"github.com/haproxytech/haproxy-consul-connect/consul"
	"github.com/hashicorp/consul/agent/connect"
	"github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
)

type SPOEHandler struct {
	c   *api.Client
	cfg func() consul.Config
}

func NewSPOEHandler(c *api.Client, cfg func() consul.Config) *SPOEHandler {
	return &SPOEHandler{
		c:   c,
		cfg: cfg,
	}
}

func (h *SPOEHandler) Handler(args []spoe.Message) ([]spoe.Action, error) {
	cfg := h.cfg()
	for _, m := range args {
		if m.Name != "check-intentions" {
			continue
		}

		certBytes, ok := m.Args["cert"].([]byte)
		if !ok {
			return nil, fmt.Errorf("spoe handler: expected cert bytes in message, got: %+v", m.Args)
		}

		cert, err := x509.ParseCertificate(certBytes)
		if err != nil {
			return nil, errors.Wrap(err, "spoe handler")
		}

		sourceApp := ""

		certURI, err := connect.ParseCertURI(cert.URIs[0])
		if err != nil {
			log.Printf("connect: invalid leaf certificate URI")
			return nil, errors.New("connect: invalid leaf certificate URI")
		}

		// Perform AuthZ
		resp, err := h.c.Agent().ConnectAuthorize(&api.AgentAuthorizeParams{
			Target:           cfg.ServiceName,
			ClientCertURI:    certURI.URI().String(),
			ClientCertSerial: connect.HexString(cert.SerialNumber.Bytes()),
		})
		if err != nil {
			return nil, errors.Wrap(err, "spoe handler: authz call failed")
		}

		log.Debugf("spoe: auth response from %s authorized=%v", certURI.URI().String(), resp.Authorized)

		authorized := resp.Authorized
		if sis, ok := certURI.(*connect.SpiffeIDService); ok {
			sourceApp = sis.Service
		}

		res := 1
		if !authorized {
			res = 0
		}
		return []spoe.Action{
			spoe.ActionSetVar{
				Name:  "auth",
				Scope: spoe.VarScopeSession,
				Value: res,
			},
			spoe.ActionSetVar{
				Name:  "source_app",
				Scope: spoe.VarScopeSession,
				Value: sourceApp,
			},
		}, nil
	}
	return nil, nil
}
