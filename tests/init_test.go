// Copyright (C) 2019 gyee authors
//
// This file is part of the gyee library.
//
// The gyee library is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The gyee library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with the gyee library.  If not, see <http://www.gnu.org/licenses/>.

package tests

import (
	"os"
	"os/signal"
	"testing"

	"github.com/yeeco/gyee/config"
	"github.com/yeeco/gyee/log"
	"github.com/yeeco/gyee/p2p"
)

// for debug by liyy, 20190409
func TestLiyy(t *testing.T) {
	t.Skip()
	liyy()
}

func liyy() {
	cfg := config.GetDefaultConfig()
	cfg.P2p.NatType = "upnp"
	cfg.P2p.GatewayIp = "192.168.1.1"
	p2p, err := p2p.NewOsnServiceWithCfg(cfg)
	if err != nil {
		log.Debug("liyy: NewOsnServiceWithCfg failed")
		return
	}

	p2p.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	<-sig

	p2p.Stop()
}
