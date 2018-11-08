package dpos

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/haxicode/go-ethereum/common"
	"github.com/haxicode/go-ethereum/core/state"
	"github.com/haxicode/go-ethereum/core/types"
	"github.com/haxicode/go-ethereum/log"
	"github.com/haxicode/go-ethereum/trie"
)

type EpochContext struct {
	TimeStamp   int64
	DposContext *types.DposContext
	statedb     *state.StateDB
}

/*投票算法
return : 返回投票人对应候选人字典
		{"0xfdb9694b92a33663f89c1fe8fcb3bd0bf07a9e09":18000}
*/
func (ec *EpochContext) countVotes() (votes map[common.Address]*big.Int, err error) {
	votes = map[common.Address]*big.Int{}

	//获取投票人列表，候选人列表，及用户基本信息列表
	delegateTrie := ec.DposContext.DelegateTrie()
	candidateTrie := ec.DposContext.CandidateTrie()
	statedb := ec.statedb

	//迭代器获取候选人列表迭代
	iterCandidate := trie.NewIterator(candidateTrie.NodeIterator(nil))
	existCandidate := iterCandidate.Next()
	if !existCandidate {
		return votes, errors.New("no candidates")
	}
	// 遍历候选人列表
	for existCandidate {
		candidate := iterCandidate.Value   //获取每个候选人--bytes
		candidateAddr := common.BytesToAddress(candidate) // 将bytes转化为地址
		delegateIterator := trie.NewIterator(delegateTrie.PrefixIterator(candidate))   //通过候选人找到每一个候选人对应投票信息列表
		existDelegator := delegateIterator.Next()                                     //调用迭代器Next()判断迭代器
		if !existDelegator {                                                          //如果在候选人列表中为空
			votes[candidateAddr] = new(big.Int)                                       //在投票人隐射中追加候选人信息
			existCandidate = iterCandidate.Next()
			continue
		}
		for existDelegator {                                                         //遍历候选人对应投票人信息列表
			delegator := delegateIterator.Value                                      //获取候选人地址
			score, ok := votes[candidateAddr]                                        //获取候选人投票权重
			if !ok {
				score = new(big.Int)                                                 //当没有查询到投票人信息时将定义一个局部遍历score
			}
			delegatorAddr := common.BytesToAddress(delegator)                        //将投票人bytes类型转换为address
			// 获取投票人的余额作为票数累积到候选人的票数中
			weight := statedb.GetBalance(delegatorAddr)
			score.Add(score, weight)
			votes[candidateAddr] = score
			existDelegator = delegateIterator.Next()
		}
		existCandidate = iterCandidate.Next()
	}
	return votes, nil
}

//剔除验证人算法
func (ec *EpochContext) kickoutValidator(epoch int64,genesis *types.Header) error {
	validators, err := ec.DposContext.GetValidators()


	//var maxValidatorSize int64
	//var safeSize int64
	fmt.Println("++++++++++++++++++++++++++9999++++++++++++++++++++++\n")
	fmt.Println("kickoutValidator test")
	maxValidatorSize := genesis.MaxValidatorSize
	safeSize := int(maxValidatorSize*2/3+1)

	if err != nil {
		return fmt.Errorf("failed to get validator: %s", err)
	}
	if len(validators) == 0 {
		return errors.New("no validator could be kickout")
	}

	epochDuration := epochInterval
	fmt.Println("0000000000000000000",epochDuration,"00000000000000\n")
	blockInterval := genesis.BlockInterval
	// First epoch duration may lt epoch interval,
	// while the first block time wouldn't always align with epoch interval,
	// so caculate the first epoch duartion with first block time instead of epoch interval,
	// prevent the validators were kickout incorrectly.
	if ec.TimeStamp-timeOfFirstBlock < epochInterval {
		epochDuration = ec.TimeStamp - timeOfFirstBlock
	}

	needKickoutValidators := sortableAddresses{}
	for _, validator := range validators {
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, uint64(epoch))
		key = append(key, validator.Bytes()...)
		cnt := int64(0)
		if cntBytes := ec.DposContext.MintCntTrie().Get(key); cntBytes != nil {
			cnt = int64(binary.BigEndian.Uint64(cntBytes))
		}

		if cnt < epochDuration/int64(blockInterval)/ int64(maxValidatorSize) /2 {
			// not active validators need kickout
			needKickoutValidators = append(needKickoutValidators, &sortableAddress{validator, big.NewInt(cnt)})
		}
	}
	// no validators need kickout
	needKickoutValidatorCnt := len(needKickoutValidators)
	if needKickoutValidatorCnt <= 0 {
		return nil
	}
	sort.Sort(sort.Reverse(needKickoutValidators))

	candidateCount := 0
	iter := trie.NewIterator(ec.DposContext.CandidateTrie().NodeIterator(nil))
	for iter.Next() {
		candidateCount++
		if candidateCount >= needKickoutValidatorCnt+int(safeSize) {
			break
		}
	}

	for i, validator := range needKickoutValidators {
		// ensure candidate count greater than or equal to safeSize
		if candidateCount <= int(safeSize) {
			log.Info("No more candidate can be kickout", "prevEpochID", epoch, "candidateCount", candidateCount, "needKickoutCount", len(needKickoutValidators)-i)
			return nil
		}

		if err := ec.DposContext.KickoutCandidate(validator.address); err != nil {
			return err
		}
		// if kickout success, candidateCount minus 1
		candidateCount--
		log.Info("Kickout candidate", "prevEpochID", epoch, "candidate", validator.address.String(), "mintCnt", validator.weight.String())
	}
	return nil
}

//实时检查出块者是否是本节点
func (ec *EpochContext) lookupValidator(now int64, blockInterval uint64) (validator common.Address, err error) {
	validator = common.Address{}
	offset := now % epochInterval
	if offset%int64(blockInterval) != 0 {    //判断当前时间是否在出块周期内
		return common.Address{}, ErrInvalidMintBlockTime
	}
	offset /= int64(blockInterval)

	validators, err := ec.DposContext.GetValidators()
	if err != nil {
		return common.Address{}, err
	}
	validatorSize := len(validators)
	if validatorSize == 0 {
		return common.Address{}, errors.New("failed to lookup validator")
	}
	offset %= int64(validatorSize)
	return validators[offset], nil
}

type sortableAddress struct {
	address common.Address
	weight  *big.Int
}
type sortableAddresses []*sortableAddress

func (p sortableAddresses) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p sortableAddresses) Len() int      { return len(p) }
func (p sortableAddresses) Less(i, j int) bool {
	if p[i].weight.Cmp(p[j].weight) < 0 {
		return false
	} else if p[i].weight.Cmp(p[j].weight) > 0 {
		return true
	} else {
		return p[i].address.String() < p[j].address.String()
	}
}
