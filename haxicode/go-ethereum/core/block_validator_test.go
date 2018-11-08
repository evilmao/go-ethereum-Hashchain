// Copyright 2015 The go-ethereum Authors
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

package core

import (
	"runtime"
	"testing"
	"time"

	"github.com/haxicode/go-ethereum/core/types"
	"github.com/haxicode/go-ethereum/core/vm"
	"github.com/haxicode/go-ethereum/ethdb"
	"github.com/haxicode/go-ethereum/params"
	"github.com/haxicode/go-ethereum/consensus/dpos"
	"github.com/haxicode/go-ethereum/common"
	"math/big"
)

// Tests that simple header verification works, for both good and bad blocks.
func TestHeaderVerification(t *testing.T) {
	dposcfg := 	&params.DposConfig {
		Validators: []common.Address{
			common.HexToAddress("0x3645b2bc6febc23d6634cc4114627c2b57b7dbb596c1bbb26af7ed9c4e57f370"),
			common.HexToAddress("0x7bf279be14c6928b0ae372f82016138a49e80a146853bc5de45ba30069ef58a9"),
		},
	}
	chainCfg := &params.ChainConfig {Dpos:dposcfg}
	// Create a simple chain to verify
	var (
		testdb    = ethdb.NewMemDatabase()
		gspec     = &Genesis{Config: chainCfg,Difficulty: big.NewInt(1),}
		genesis   = gspec.MustCommit(testdb)
		blocks, _ = GenerateChain(params.TestChainConfig, genesis, dpos.New(chainCfg.Dpos,testdb), testdb, 8, nil)
	)
	headers := make([]*types.Header, len(blocks))
	for i, block := range blocks {
		headers[i] = block.Header()
	}
	// Run the header checker for blocks one-by-one, checking for both valid and invalid nonces
	chain, _ := NewBlockChain(testdb, nil, params.TestChainConfig, dpos.New(chainCfg.Dpos,testdb), vm.Config{})
	defer chain.Stop()

	for i := 0; i < len(blocks); i++ {
			var results <-chan error
				engine :=dpos.New(chainCfg.Dpos,testdb)
				_, results = engine.VerifyHeaders(chain, []*types.Header{headers[i]}, []bool{true})
			// Make sure no more data is returned
			select {
			case result := <-results:
				if result !=nil{
				  t.Fatalf("test %d: unexpected result returned: %v", i, result)
				}
			case <-time.After(25 * time.Millisecond):
			}

		chain.InsertChain(blocks[i : i+1])
	}
}

// Tests that concurrent header verification works, for both good and bad blocks.
func TestHeaderConcurrentVerification2(t *testing.T)  { testHeaderConcurrentVerification(t, 2) }
func TestHeaderConcurrentVerification8(t *testing.T)  { testHeaderConcurrentVerification(t, 8) }
func TestHeaderConcurrentVerification32(t *testing.T) { testHeaderConcurrentVerification(t, 32) }

func testHeaderConcurrentVerification(t *testing.T, threads int) {
	dposcfg := 	&params.DposConfig {
		Validators: []common.Address{
			common.HexToAddress("0x3645b2bc6febc23d6634cc4114627c2b57b7dbb596c1bbb26af7ed9c4e57f370"),
			common.HexToAddress("0x7bf279be14c6928b0ae372f82016138a49e80a146853bc5de45ba30069ef58a9"),
		},
	}
	chainCfg := &params.ChainConfig {Dpos:dposcfg}
	// Create a simple chain to verify
	var (
		testdb    = ethdb.NewMemDatabase()
		gspec     = &Genesis{Config: chainCfg,Difficulty: big.NewInt(1),}
		genesis   = gspec.MustCommit(testdb)
		blocks, _ = GenerateChain(params.TestChainConfig, genesis,  dpos.New(chainCfg.Dpos,testdb), testdb, 8, nil)
	)
	headers := make([]*types.Header, len(blocks))
	seals := make([]bool, len(blocks))

	for i, block := range blocks {
		headers[i] = block.Header()
		seals[i] = true
	}
	// Set the number of threads to verify on
	old := runtime.GOMAXPROCS(threads)
	defer runtime.GOMAXPROCS(old)

	// Run the header checker for the entire block chain at once both for a valid and
	// also an invalid chain (enough if one arbitrary block is invalid).
	for i, valid := range []bool{true, false} {
		var results <-chan error

		if valid {
			chain, _ := NewBlockChain(testdb, nil, params.TestChainConfig, dpos.New(chainCfg.Dpos,testdb), vm.Config{})
			_, results = chain.engine.VerifyHeaders(chain, headers, seals)
			chain.Stop()
		} else {
			chain, _ := NewBlockChain(testdb, nil, params.TestChainConfig, dpos.New(chainCfg.Dpos,testdb), vm.Config{})
			_, results = chain.engine.VerifyHeaders(chain, headers, seals)
			chain.Stop()
		}
		// Wait for all the verification results
		checks := make(map[int]error)
		for j := 0; j < len(blocks); j++ {
			select {
			case result := <-results:
				checks[j] = result
			case <-time.After(time.Second):
				t.Fatalf("test %d.%d: verification timeout", i, j)
			}
		}
		// Check nonce check validity
		for j := 0; j < len(blocks); j++ {
			want := valid || (j < len(blocks))
			if (checks[j] == nil) != want {
				t.Errorf("test %d.%d: validity mismatch: have %v, want %v", i, j, checks[j], want)
			}
			if !want {
				// A few blocks after the first error may pass verification due to concurrent
				// workers. We don't care about those in this test, just that the correct block
				// errors out.
				break
			}
		}
		// Make sure no more data is returned
		select {
		case result := <-results:
			t.Fatalf("test %d: unexpected result returned: %v", i, result)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Tests that aborting a header validation indeed prevents further checks from being
// run, as well as checks that no left-over goroutines are leaked.
func TestHeaderConcurrentAbortion2(t *testing.T)  { testHeaderConcurrentAbortion(t, 2) }
func TestHeaderConcurrentAbortion8(t *testing.T)  { testHeaderConcurrentAbortion(t, 8) }
func TestHeaderConcurrentAbortion32(t *testing.T) { testHeaderConcurrentAbortion(t, 32) }


func testHeaderConcurrentAbortion(t *testing.T, threads int) {
	dposcfg := 	&params.DposConfig {
		Validators: []common.Address{
			common.HexToAddress("0x3645b2bc6febc23d6634cc4114627c2b57b7dbb596c1bbb26af7ed9c4e57f370"),
			common.HexToAddress("0x7bf279be14c6928b0ae372f82016138a49e80a146853bc5de45ba30069ef58a9"),
		},
	}
	chainCfg := &params.ChainConfig {Dpos:dposcfg}
	// Create a simple chain to verify
	var (
		testdb    = ethdb.NewMemDatabase()
		gspec     = &Genesis{Config: chainCfg,Difficulty: big.NewInt(1)}
		genesis   = gspec.MustCommit(testdb)
		blocks, _ = GenerateChain(params.TestChainConfig, genesis, dpos.New(chainCfg.Dpos,testdb), testdb, 1024, nil)
	)
	headers := make([]*types.Header, len(blocks))
	seals := make([]bool, len(blocks))

	for i, block := range blocks {
		headers[i] = block.Header()
		seals[i] = true
	}
	// Set the number of threads to verify on
	old := runtime.GOMAXPROCS(threads)
	defer runtime.GOMAXPROCS(old)

	// Start the verifications and immediately abort
	chain, _ := NewBlockChain(testdb, nil, params.TestChainConfig, dpos.New(chainCfg.Dpos,testdb), vm.Config{})

	//Add  for TestVerifySeal
	config := &params.DposConfig{
		Validators: []common.Address{common.HexToAddress("0x")},
	}
	consensus := dpos.New(config, ethdb.NewMemDatabase())
	currentHeader := chain.hc.CurrentHeader()
	block := chain.GetBlock(currentHeader.Hash(), currentHeader.Number.Uint64())
	consensus.VerifySeal(chain, block.Header())

	defer chain.Stop()

	abort, results := chain.engine.VerifyHeaders(chain, headers, seals)
	close(abort)

	// Deplete the results channel
	verified := 0
	for depleted := false; !depleted; {
		select {
		case result := <-results:
			if result != nil {
				t.Errorf("header %d: validation failed: %v", verified, result)
			}
			verified++
		case <-time.After(50 * time.Millisecond):
			depleted = true
		}
	}
	// Check that abortion was honored by not processing too many POWs
	if verified > 2*threads {
		t.Errorf("verification count too large: have %d, want below %d", verified, 2*threads)
	}
}
