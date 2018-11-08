// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package light

import (
	"context"
	"math/big"
	"testing"

	"github.com/haxicode/go-ethereum/common"
	"github.com/haxicode/go-ethereum/consensus/dpos"
	"github.com/haxicode/go-ethereum/core"
	"github.com/haxicode/go-ethereum/core/rawdb"
	"github.com/haxicode/go-ethereum/core/types"
	"github.com/haxicode/go-ethereum/ethdb"
	"github.com/haxicode/go-ethereum/params"
	"fmt"
)

// So we can deterministically seed different blockchains
var (
	canonicalSeed = 1
	forkSeed      = 2
	extraVanity        = 32   // Fixed number of extra-data prefix bytes reserved for signer vanity
	extraSeal          = 65   // Fixed number of extra-data suffix bytes reserved for signer seal
)

// makeHeaderChain creates a deterministic chain of headers rooted at parent.
func makeBlockChain(parent *types.Block, n int, db ethdb.Database, seed int) []*types.Header {
	blocks, _ := core.GenerateChain(params.TestChainConfig, parent, dpos.New(params.DposChainConfig.Dpos,db), db, n, func(i int, b *core.BlockGen) {
		b.SetCoinbase(common.Address{0: byte(seed), 19: byte(i)})
	})
	headers := make([]*types.Header, len(blocks))
	for i, block := range blocks {
		headers[i] = block.Header()
	}
	return headers
}
// newCanonical creates a chain database, and injects a deterministic canonical
// chain. Depending on the full flag, if creates either a full block chain or a
// header only chain.
func newCanonical(n int) (ethdb.Database, *LightChain, error) {
	dposcfg := 	&params.DposConfig {
		Validators: []common.Address{
			common.HexToAddress("0x3645b2bc6febc23d6634cc4114627c2b57b7dbb596c1bbb26af7ed9c4e57f370"),
			common.HexToAddress("0x7bf279be14c6928b0ae372f82016138a49e80a146853bc5de45ba30069ef58a9"),
		},
	}
	chainCfg := &params.ChainConfig {Dpos:dposcfg}
	db := ethdb.NewMemDatabase()
	gspec := core.Genesis{
		ExtraData:  make([]byte, extraVanity+extraSeal),
		Config: chainCfg,
		Difficulty: big.NewInt(1),}
	genesis := gspec.MustCommit(db)
	blockchain, _ := NewLightChain(&dummyOdr{db: db}, gspec.Config, dpos.New(params.DposChainConfig.Dpos,db))
	blockchain.genesisBlock.DposContext=genesis.DposContext
	// Create and inject the requested chain
	if n == 0 {
		return db, blockchain, nil
	}
	// Header-only chain requested
	headers := makeBlockChain(genesis, n, db, canonicalSeed)
	_, err := blockchain.InsertHeaderChain(headers, 1)
	return db, blockchain, err
}

// newTestLightChain creates a LightChain that doesn't validate anything.
func newTestLightChain() *LightChain {
	dposcfg := 	&params.DposConfig {
		Validators: []common.Address{
			common.HexToAddress("0x3645b2bc6febc23d6634cc4114627c2b57b7dbb596c1bbb26af7ed9c4e57f370"),
			common.HexToAddress("0x7bf279be14c6928b0ae372f82016138a49e80a146853bc5de45ba30069ef58a9"),
		},
	}
	chainCfg := &params.ChainConfig {Dpos:dposcfg}
	db := ethdb.NewMemDatabase()
	gspec := &core.Genesis{
		Difficulty: big.NewInt(1),
		Config:     chainCfg,
	}
	gspec.MustCommit(db)
	lc, err := NewLightChain(&dummyOdr{db: db}, gspec.Config, dpos.New(params.DposChainConfig.Dpos,db))
	if err != nil {
		panic(err)
	}
	return lc
}

// Test fork of length N starting from block i
func testFork(t *testing.T, LightChain *LightChain, i, n int, comparator func(td1, td2 *big.Int)) {
	// Copy old chain up to #i into a new db
	db, LightChain2, err := newCanonical(i)
	if err != nil {
		t.Fatal("could not make new canonical in testFork", err)
	}
	// Assert the chains have the same header/block at #i
	var hash1, hash2 common.Hash
	hash1 = LightChain.GetHeaderByNumber(uint64(i)).Hash()
	hash2 = LightChain2.GetHeaderByNumber(uint64(i)).Hash()
	if hash1 != hash2 {
		t.Errorf("chain content mismatch at %d: have hash %v, want hash %v", i, hash2, hash1)
	}
	// Extend the newly created chain
	headerChainB := makeBlockChain(LightChain2.genesisBlock, n, db, forkSeed)
	if _, err := LightChain2.InsertHeaderChain(headerChainB, 1); err != nil {
		t.Fatalf("failed to insert forking chain: %v", err)
	}
	// Sanity check that the forked chain can be imported into the original
	var tdPre, tdPost *big.Int
	tdPre = LightChain.GetTdByHash(LightChain.CurrentHeader().Hash())
	if err := testHeaderChainImport(headerChainB, LightChain); err != nil {
		t.Fatalf("failed to import forked header chain: %v", err)
	}
	tdPost = LightChain.GetTdByHash(headerChainB[len(headerChainB)-1].Hash())
	// Compare the total difficulties of the chains
	comparator(tdPre, tdPost)
}

// testHeaderChainImport tries to process a chain of header, writing them into
// the database if successful.
func testHeaderChainImport(chain []*types.Header, lightchain *LightChain) error {
	for _, header := range chain {
		// Try and validate the header
		if err := lightchain.engine.VerifyHeader(lightchain.hc, header, true); err != nil {
			return err
		}
		// Manually insert the header into the database, but don't reorganize (allows subsequent testing)
		lightchain.mu.Lock()
		rawdb.WriteTd(lightchain.chainDb, header.Hash(), header.Number.Uint64(), new(big.Int).Add(header.Difficulty, lightchain.GetTdByHash(header.ParentHash)))
		rawdb.WriteHeader(lightchain.chainDb, header)
		lightchain.mu.Unlock()
	}
	return nil
}

// Tests that given a starting canonical chain of a given size, it can be extended
// with various length chains.
func TestExtendCanonicalHeaders(t *testing.T) {
	length := 5

	// Make first chain starting from genesis
	_, processor, err := newCanonical(length)
	if err != nil {
		t.Fatalf("failed to make new canonical chain: %v", err)
	}
	// Define the difficulty comparator
	better := func(td1, td2 *big.Int) {
		if td2.Cmp(td1) <= 0 {
			t.Errorf("total difficulty mismatch: have %v, expected more than %v", td2, td1)
		}
	}
	// Start fork from current height
	testFork(t, processor, length, 1, better)
	testFork(t, processor, length, 2, better)
	testFork(t, processor, length, 5, better)
	testFork(t, processor, length, 10, better)
}

// Tests that given a starting canonical chain of a given size, creating shorter
// forks do not take canonical ownership.
func TestShorterForkHeaders(t *testing.T) {
	length := 10

	// Make first chain starting from genesis
	_, processor, err := newCanonical(length)
	if err != nil {
		t.Fatalf("failed to make new canonical chain: %v", err)
	}
	// Define the difficulty comparator
	worse := func(td1, td2 *big.Int) {
		if td2.Cmp(td1) <= 0  {
			t.Errorf("total difficulty mismatch: have %v, expected less than %v", td2, td1)
		}
	}
	// Sum of numbers must be less than `length` for this to be a shorter fork
	testFork(t, processor, 0, 3, worse)
	testFork(t, processor, 0, 7, worse)
	testFork(t, processor, 1, 1, worse)
	testFork(t, processor, 1, 7, worse)
	testFork(t, processor, 5, 3, worse)
	testFork(t, processor, 5, 4, worse)
}

// Tests that given a starting canonical chain of a given size, creating longer
// forks do take canonical ownership.
func TestLongerForkHeaders(t *testing.T) {
	length := 10

	// Make first chain starting from genesis
	_, processor, err := newCanonical(length)
	if err != nil {
		t.Fatalf("failed to make new canonical chain: %v", err)
	}
	// Define the difficulty comparator
	better := func(td1, td2 *big.Int) {
		if td2.Cmp(td1) <= 0 {
			t.Errorf("total difficulty mismatch: have %v, expected more than %v", td2, td1)
		}
	}
	// Sum of numbers must be greater than `length` for this to be a longer fork
	testFork(t, processor, 0, 11, better)
	testFork(t, processor, 0, 15, better)
	testFork(t, processor, 1, 10, better)
	testFork(t, processor, 1, 12, better)
	testFork(t, processor, 5, 6, better)
	testFork(t, processor, 5, 8, better)
}

// Tests that given a starting canonical chain of a given size, creating equal
// forks do take canonical ownership.
func TestEqualForkHeaders(t *testing.T) {
	length := 10

	// Make first chain starting from genesis
	_, processor, err := newCanonical(length)
	if err != nil {
		t.Fatalf("failed to make new canonical chain: %v", err)
	}
	// Define the difficulty comparator
	equal := func(td1, td2 *big.Int) {
		if td2.Cmp(td1) <= 0  {
			t.Errorf("total difficulty mismatch: have %v, want %v", td2, td1)
		}
	}
	// Sum of numbers must be equal to `length` for this to be an equal fork
	testFork(t, processor, 0, 10, equal)
	testFork(t, processor, 1, 9, equal)
	testFork(t, processor, 2, 8, equal)
	testFork(t, processor, 5, 5, equal)
	testFork(t, processor, 6, 4, equal)
	testFork(t, processor, 9, 1, equal)
}

// Tests that chains missing links do not get accepted by the processor.
func TestBrokenHeaderChain(t *testing.T) {
	// Make chain starting from genesis
	db, LightChain, err := newCanonical(10)
	if err != nil {
		t.Fatalf("failed to make new canonical chain: %v", err)
	}
	// Create a forked chain, and try to insert with a missing link
	chain := makeBlockChain(LightChain.genesisBlock, 5, db, forkSeed)[1:]
	if err := testHeaderChainImport(chain, LightChain); err != nil {
		t.Errorf("broken header chain not reported")
	}
}

func makeHeaderChainWithDiff(genesis *types.Block, d []int, seed byte) []*types.Header {
	var chain []*types.Header
	for i, difficulty := range d {

		header := &types.Header{
			Coinbase:    common.Address{seed},
			Number:      big.NewInt(int64(i + 1)),
			Difficulty:  big.NewInt(int64(difficulty)),
			UncleHash:   types.EmptyUncleHash,
			TxHash:      types.EmptyRootHash,
			ReceiptHash: types.EmptyRootHash,
			Extra:        make([]byte,extraVanity+extraSeal),
			Time:         big.NewInt(int64((i + 1)*10)),
		}
		if i == 0 {
			header.ParentHash = genesis.Hash()
		} else {
			header.ParentHash = chain[i-1].Hash()
		}
		chain = append(chain, types.CopyHeader(header))
	}
	return chain
}

type dummyOdr struct {
	OdrBackend
	db ethdb.Database
}

func (odr *dummyOdr) Database() ethdb.Database {
	return odr.db
}

func (odr *dummyOdr) Retrieve(ctx context.Context, req OdrRequest) error {
	return nil
}

// Tests that the insertion functions detect banned hashes.
func TestBadHeaderHashes(t *testing.T) {
	bc := newTestLightChain()

	// Create a chain, ban a hash and try to import
	var err error
	headers := makeHeaderChainWithDiff(bc.genesisBlock, []int{1, 1, 1}, 10)
	core.BadHashes[headers[2].Hash()] = true
	if _, err = bc.InsertHeaderChain(headers, 1); err != core.ErrBlacklistedHash {
		t.Errorf("error mismatch: have: %v, want %v", err, core.ErrBlacklistedHash)
	}
}

// Tests that bad hashes are detected on boot, and the chan rolled back to a
// good state prior to the bad hash.
func TestReorgBadHeaderHashes(t *testing.T) {
	bc := newTestLightChain()

	// Create a chain, import and ban afterwards
	headers := makeHeaderChainWithDiff(bc.genesisBlock, []int{1, 1, 1, 1}, 10)

	if _, err := bc.InsertHeaderChain(headers, 1); err != nil {
		t.Fatalf("failed to import headers: %v", err)
	}
	if bc.CurrentHeader().Hash() != headers[3].Hash() {
		t.Errorf("last header hash mismatch: have: %x, want %x", bc.CurrentHeader().Hash(), headers[3].Hash())
	}
	core.BadHashes[headers[3].Hash()] = true
	defer func() { delete(core.BadHashes, headers[3].Hash()) }()

	// Create a new LightChain and check that it rolled back the state.
	ncm, err := NewLightChain(&dummyOdr{db: bc.chainDb}, params.TestChainConfig, dpos.New(params.DposChainConfig.Dpos,ethdb.NewMemDatabase()))
	if err != nil {
		t.Fatalf("failed to create new chain manager: %v", err)
	}
	if ncm.CurrentHeader().Hash() != headers[2].Hash() {
		t.Errorf("last header hash mismatch: have: %x, want %x", ncm.CurrentHeader().Hash(), headers[2].Hash())
	}
}
