/*
 *  Copyright (C) 2017 gyee authors
 *
 *  This file is part of the gyee library.
 *
 *  The gyee library is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  The gyee library is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with the gyee library.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package core

import (
	"sync/atomic"

	"github.com/ethereum/go-ethereum/rlp"
	"github.com/golang/protobuf/proto"
	"github.com/yeeco/gyee/common"
	"github.com/yeeco/gyee/core/pb"
	"github.com/yeeco/gyee/core/state"
)

// Block Header of yee chain
// Encoded with RLP into byte[] for hashing
// stored as value in Storage, with hash as key
type BlockHeader struct {
	// chain
	ChainID    uint32      `json:"chainID"`
	Number     uint64      `json:"number"`
	ParentHash common.Hash `json:"parentHash"`

	// trie root hashes
	StateRoot    common.Hash `json:"stateRoot"`
	TxsRoot      common.Hash `json:"transactionsRoot"`
	ReceiptsRoot common.Hash `json:"receiptsRoot"`

	// block time in milli seconds
	Time int64 `json:"timestamp"`

	// extra binary data
	Extra []byte `json:"extraData"`
}

func CopyHeader(header *BlockHeader) *BlockHeader {
	cpy := *header
	return &cpy
}

func (bh *BlockHeader) toSignedProto() (*corepb.SignedBlockHeader, error) {
	enc, err := rlp.EncodeToBytes(bh)
	if err != nil {
		return nil, err
	}
	// TODO: bloom signature
	return &corepb.SignedBlockHeader{
		Header: enc,
	}, nil
}

// In-memory representative for the block concept
type Block struct {
	// header
	header    *BlockHeader
	signature *corepb.SignedBlockHeader

	stateTrie    *state.AccountTrie
	transactions Transactions
	// TODO: receipts

	// cache
	hash atomic.Value
}

func NewBlock(header *BlockHeader, txs []*Transaction) *Block {
	b := &Block{header: CopyHeader(header)}

	if len(txs) == 0 {
		// TODO: header TxsRoot for empty txs
	} else {
		// TODO: header TxsRoot for txs
		b.transactions = make([]*Transaction, len(txs))
		copy(b.transactions, txs)
	}

	return b
}

func (b *Block) Seal() {
}

func (b *Block) ToBytes() ([]byte, error) {
	pbSignedHeader, err := b.header.toSignedProto()
	if err != nil {
		return nil, err
	}
	pbBlock := &corepb.Block{
		Header: pbSignedHeader,
		// TODO: txs
	}
	enc, err := proto.Marshal(pbBlock)
	if err != nil {
		return nil, err
	}
	return enc, nil
}

func (b *Block) setBytes(enc []byte) error {
	pbBlock := &corepb.Block{}
	if err := proto.Unmarshal(enc, pbBlock); err != nil {
		return err
	}
	header := new(BlockHeader)
	if err := rlp.DecodeBytes(pbBlock.Header.Header, header); err != nil {
		return err
	}
	b.header = header
	// TODO: txs
	return nil
}

func ParseBlock(enc []byte) (*Block, error) {
	b := new(Block)
	if err := b.setBytes(enc); err != nil {
		return nil, err
	}
	return b, nil
}
