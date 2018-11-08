// Copyright 2017 The go-ethereum Authors
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

package dpos

import (
	"encoding/binary"
	"errors"
	"github.com/haxicode/go-ethereum/common"
	"github.com/haxicode/go-ethereum/consensus"
	"github.com/haxicode/go-ethereum/core/types"
	"github.com/haxicode/go-ethereum/crypto"
	"github.com/haxicode/go-ethereum/log"
	"github.com/haxicode/go-ethereum/rpc"
	"github.com/haxicode/go-ethereum/trie"
	"math/rand"
	"sort"
	"fmt"

	"math/big"
)

// API is a user facing RPC API to allow controlling the delegate and voting
// mechanisms of the delegated-proof-of-stake
type API struct {
	chain consensus.ChainReader
	dpos  *Dpos
}

// GetValidators retrieves the list of the validators at specified block
func (api *API) GetValidators(number *rpc.BlockNumber) ([]common.Address, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	if header == nil {
		return nil, errUnknownBlock
	}

	trieDB := trie.NewDatabase(api.dpos.db)
	epochTrie, err := types.NewEpochTrie(header.DposContext.EpochHash, trieDB)

	if err != nil {
		return nil, err
	}
	dposContext := types.DposContext{}
	dposContext.SetEpoch(epochTrie)
	validators, err := dposContext.GetValidators()
	if err != nil {
		return nil, err
	}
	return validators, nil
}

// GetConfirmedBlockNumber retrieves the latest irreversible block
func (api *API) GetConfirmedBlockNumber() (*big.Int, error) {
	var err error
	header := api.dpos.confirmedBlockHeader
	if header == nil {
		header, err = api.dpos.loadConfirmedBlockHeader(api.chain)
		if err != nil {
			return nil, err
		}
	}
	return header.Number, nil
}
func (ec *EpochContext) tryElect(genesis, parent *types.Header) error {

	genesisEpoch := genesis.Time.Int64() / epochInterval   //genesisEpoch is 0
	prevEpoch := parent.Time.Int64() / epochInterval
	currentEpoch := ec.TimeStamp / epochInterval

	prevEpochIsGenesis := prevEpoch == genesisEpoch  		// bool type
	if prevEpochIsGenesis && prevEpoch < currentEpoch {
		prevEpoch = currentEpoch - 1
	}

	prevEpochBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(prevEpochBytes, uint64(prevEpoch))
	iter := trie.NewIterator(ec.DposContext.MintCntTrie().PrefixIterator(prevEpochBytes))
	from_genesis_maxsize :=  genesis.MaxValidatorSize
	fmt.Print("+++++++++++++++++++6666666666666666666++++++++++++++++++++++++++++\n")
	fmt.Print(from_genesis_maxsize)
	// 根据当前块和上一块的时间计算当前块和上一块是否属于同一个周期，
	// 如果是同一个周期，意味着当前块不是周期的第一块，不需要触发选举
	// 如果不是同一周期，说明当前块是该周期的第一块，则触发选举
	fmt.Print("+++++++++++++++++++8888888++++++++++++++++++++++++++++\n")
	fmt.Print("Genesis init get maxvalidatorsize to kickoutValidator")
	for i := prevEpoch; i < currentEpoch; i++ {
		// if prevEpoch is not genesis, kickout not active candidate
		// 如果前一个周期不是创世周期，触发踢出候选人规则
		// 踢出规则主要是看上一周期是否存在候选人出块少于特定阈值(50%), 如果存在则踢出
		if !prevEpochIsGenesis && iter.Next() {
			if err := ec.kickoutValidator(prevEpoch,genesis); err != nil {
				return err
			}
		}
		// 对候选人进行计票后按照票数由高到低来排序, 选出前 N 个
		// 这里需要注意的是当前对于成为候选人没有门槛限制很容易被恶意攻击
		votes, err := ec.countVotes()
		if err != nil {
			return err
		}
		//add
		maxValidatorSize := int(genesis.MaxValidatorSize)
		safeSize := maxValidatorSize*2/3+1
		candidates := sortableAddresses{}
		for candidate, cnt := range votes {
			candidates = append(candidates, &sortableAddress{candidate, cnt})
		}
		if len(candidates) < safeSize {
			//fmt.Print("whteaaa!!!!!",safeSize)
			return errors.New("too few candidates")
		}
		sort.Sort(candidates)
		if len(candidates) > maxValidatorSize {
			candidates = candidates[:maxValidatorSize]
		}

		// shuffle candidates
		// 打乱验证人列表，由于使用 seed 是由父块的 hash 以及当前周期编号组成，
		// 所以每个节点计算出来的验证人列表也会一致
		seed := int64(binary.LittleEndian.Uint32(crypto.Keccak512(parent.Hash().Bytes()))) + i
		r := rand.New(rand.NewSource(seed))
		for i := len(candidates) - 1; i > 0; i-- {
			j := int(r.Int31n(int32(i + 1)))
			candidates[i], candidates[j] = candidates[j], candidates[i]
		}
		sortedValidators := make([]common.Address, 0)
		for _, candidate := range candidates {
			sortedValidators = append(sortedValidators, candidate.address)
		}

		epochTrie, _ := types.NewEpochTrie(common.Hash{}, ec.DposContext.DB())
		ec.DposContext.SetEpoch(epochTrie)
		ec.DposContext.SetValidators(sortedValidators)
		log.Info("Come to new epoch", "prevEpoch", i, "nextEpoch", i+1)
	}
	return nil
}




