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
	"io/ioutil"
	"math/big"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yeeco/gyee/common"
	"github.com/yeeco/gyee/common/address"
	"github.com/yeeco/gyee/config"
	"github.com/yeeco/gyee/core"
	"github.com/yeeco/gyee/crypto"
	"github.com/yeeco/gyee/crypto/secp256k1"
	"github.com/yeeco/gyee/log"
	"github.com/yeeco/gyee/node"
	"github.com/yeeco/gyee/p2p"
)

const testChainID = uint32(1)

// build up a chain with random created validators
func TestInit(t *testing.T) {
	doTest(t, 16, 30*time.Second, nil)
}

func TestInitWithTx(t *testing.T) {
	numNodes := uint(16)
	doTest(t, numNodes, 300*time.Second, func(quitCh chan struct{}, wg sync.WaitGroup, nodes []*node.Node) {
		genTestTxs(t, quitCh, wg, nodes, numNodes)
	})
}

func genTestTxs(t *testing.T,
	quitCh chan struct{}, wg sync.WaitGroup,
	nodes []*node.Node, numNodes uint) {
	wg.Add(1)
	defer wg.Done()

	totalTxs := int(0)
	addrs := make([]common.Address, numNodes)
	signers := make([]crypto.Signer, numNodes)
	nonces := make([]uint64, numNodes)
	for i, n := range nodes {
		addrs[i] = *n.Core().MinerAddr().CommonAddress()
		if signer, err := n.Core().GetMinerSigner(); err != nil {
			t.Fatalf("signer failed %v", err)
		} else {
			signers[i] = signer
		}
	}
	time.Sleep(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	log.Info("send tx start")
Exit:
	for {
		for i, fn := range nodes {
			select {
			case <-quitCh:
				ticker.Stop()
				break Exit
			case <-ticker.C:
				// send txs
			}
			for j, tn := range nodes {
				if j == i {
					continue
				}
				// transfer f => t
				tAddr := &addrs[j]
				tx := core.NewTransaction(testChainID, nonces[i], tAddr, big.NewInt(100))
				if err := tx.Sign(signers[i]); err != nil {
					t.Errorf("sign failed %v", err)
					continue
				}
				data, err := tx.Encode()
				if err != nil {
					t.Errorf("encode tx failed %v", err)
					continue
				}
				nonces[i]++
				go func(hash *common.Hash, data []byte) {
					msg := &p2p.Message{
						MsgType: p2p.MessageTypeTx,
						Data:    data,
					}
					_ = fn.P2pService().DhtSetValue(hash[:], data)
					_ = fn.P2pService().BroadcastMessage(*msg)
					fn.Core().FakeP2pRecv(msg)
					tn.Core().FakeP2pRecv(msg)
				}(tx.Hash(), data)
				totalTxs++
			}
		}
		log.Info("Total txs sent", totalTxs)
	}
	log.Info("Total txs sent", totalTxs)
}

func doTest(t *testing.T, numNodes uint, duration time.Duration,
	coroutine func(chan struct{}, sync.WaitGroup, []*node.Node)) {
	var (
		quitCh = make(chan struct{})
		wg     sync.WaitGroup
	)
	tmpDir, err := ioutil.TempDir("", "yee-test-")
	if err != nil {
		t.Fatalf("TempDir() %v", err)
	}
	keys := genKeys(numNodes)
	genesis := genGenesis(t, keys)
	nodes := make([]*node.Node, 0, numNodes)
	for i, key := range keys {
		cfg := dftConfig(filepath.Join(tmpDir, strconv.Itoa(i)))
		cfg.Chain.Key = key

		n := genNode(t, cfg, genesis)
		if err := n.Start(); err != nil {
			t.Fatalf("node start %v", err)
		}
		nodes = append(nodes, n)
	}
	for i, n := range nodes {
		log.Info("validator", "index", i, "addr", n.Core().MinerAddr())
	}
	const numViewers = 2
	viewers := make([]*node.Node, 0, numViewers)
	for i := 0; i < numViewers; i++ {
		cfg := dftConfig(filepath.Join(tmpDir, "v"+strconv.Itoa(i)))
		cfg.Chain.Mine = false

		n := genNode(t, cfg, genesis)
		if err := n.Start(); err != nil {
			t.Fatalf("node start %v", err)
		}
		viewers = append(viewers, n)
	}
	if coroutine != nil {
		go coroutine(quitCh, wg, nodes)
	}
	time.Sleep(duration)
	close(quitCh)
	wg.Wait()
	// check node chains
	for height := uint64(0); ; height++ {
		var (
			lastBlock *core.Block
			reached   = int(0)
			mismatch  = int(0)
		)
		for i, n := range nodes {
			if height > n.Core().Chain().CurrentBlockHeight() {
				continue
			}
			b := n.Core().Chain().GetBlockByNumber(height)
			if b == nil {
				log.Error("block not found", "idx", i, "node", n, "height", height)
				t.Errorf("block not found idx %d height %d", i, height)
				continue
			}
			if lastBlock == nil {
				lastBlock = b
				reached++
			} else {
				if lastBlock.Hash() != b.Hash() {
					log.Error("block mismatch", "idx", i, "height", height)
					t.Errorf("block mismatch idx %d height %d", i, height)
					mismatch++
				} else {
					reached++
				}
			}
		}
		if lastBlock == nil {
			// no node reached
			break
		}
		log.Info("chain check", "height", height, "hash", lastBlock.Hash())
		log.Info("    stats", "reached", reached, "mismatch", mismatch)
	}
	// stop nodes
	for _, n := range nodes {
		_ = n.Stop()
	}
}

func genNode(t *testing.T, cfg *config.Config, genesis *core.Genesis) *node.Node {
	p2pSvc, err := p2p.NewInmemService()
	if err != nil {
		t.Fatalf("newP2P %v", err)
	}
	n, err := node.NewNodeWithGenesis(cfg, genesis, p2pSvc)
	if err != nil {
		t.Fatalf("newNode() %v", err)
	}
	return n
}

func genKeys(count uint) [][]byte {
	ret := make([][]byte, 0, count)
	for i := uint(0); i < count; i++ {
		key := secp256k1.NewPrivateKey()
		ret = append(ret, key)
	}
	return ret
}

func genGenesis(t *testing.T, keys [][]byte) *core.Genesis {
	count := len(keys)
	initDist := make(map[string]*big.Int)
	validators := make([]string, 0, count)
	for _, key := range keys {
		pub, err := secp256k1.GetPublicKey(key)
		if err != nil {
			t.Fatalf("GetPublicKey() %v", err)
		}
		addr, err := address.NewAddressFromPublicKey(pub)
		if err != nil {
			t.Fatalf("NewAddressFromPublicKey() %v", err)
		}
		addrStr := addr.String()
		// setup init dist
		initDist[addrStr] = big.NewInt(1000000000)
		// setup validator
		validators = append(validators, addrStr)
	}
	genesis, err := core.NewGenesis(core.ChainID(testChainID), initDist, validators)
	if err != nil {
		t.Fatalf("NewGenesis() %v", err)
	}
	return genesis
}

func dftConfig(nodeDir string) *config.Config {
	cfg := &config.Config{
		Chain: &config.ChainConfig{
			ChainID: testChainID,
			Mine:    true,
		},
		Rpc: &config.RpcConfig{},
	}
	cfg.NodeDir = nodeDir

	return cfg
}
