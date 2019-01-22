/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statemanager

import (
	"encoding/hex"
	"fmt"
	"reflect"

	"github.com/go-stack/stack"
	"github.com/hyperledger/burrow/acm"
	"github.com/hyperledger/burrow/acm/state"
	"github.com/hyperledger/burrow/binary"
	"github.com/hyperledger/burrow/crypto"
	"github.com/hyperledger/burrow/execution/errors"
	"github.com/hyperledger/burrow/execution/evm"
	"github.com/hyperledger/burrow/permission"
	"github.com/hyperledger/fabric/core/chaincode/shim"
)

//Permissions for contract to send CallTx or SendTx to another contract
const ContractPermFlags = permission.Call | permission.Send

var ContractPerms = permission.AccountPermissions{
	Base: permission.BasePermissions{
		Perms:  ContractPermFlags,
		SetBit: ContractPermFlags,
	},
	Roles: []string{},
}

type StateManager interface {
	evm.Interface
	GetAccount(address crypto.Address) (*acm.Account, error)
}

type stateManager struct {
	stub shim.ChaincodeStubInterface
	// We will be looking into adding a storageCache for accounts later
	// The storageCache can be single threaded because the statemanager is 1-1 with the evm which is single threaded.
	storageCache map[string]binary.Word256
	accountCache map[string][]byte
	error        errors.CodedError
	readonly	bool
}

func NewStateManager(stub shim.ChaincodeStubInterface) StateManager {
	return &stateManager{
		stub:         stub,
		accountCache: make(map[string][]byte),
		storageCache: make(map[string]binary.Word256),
	}
}

///// ----------------------------------

func (st *stateManager) NewCache(cacheOptions ...state.CacheOption) evm.Interface {
	 newState := &stateManager{
		stub: st.stub,
		accountCache: st.accountCache,
		storageCache: st.storageCache,
	}

	for _, option := range cacheOptions {
		if reflect.ValueOf(option) == reflect.ValueOf(state.ReadOnly) {
			newState.readonly = true
		}
	}

 	return newState
}

// Stub function
func (st *stateManager) Sync() errors.CodedError {
	// Do not sync if we have erred
	if st.error != nil {
		return st.error
	}

	//err := st.storageCache.Sync(st.backend)
	//if err != nil {
	//	return errors.AsException(err)
	//}

	return nil
}

func (st *stateManager) Error() errors.CodedError {
	if st.error == nil {
		return nil
	}
	return st.error
}

func (st *stateManager) PushError(err error) {
	if st.error == nil {
		// Make sure we are not wrapping a known nil value
		ex := errors.AsException(err)
		if ex != nil {
			ex.Exception = fmt.Sprintf("%s\nStack trace: %s", ex.Exception, stack.Trace().String())
			st.error = ex
		}
	}
}

// Reader

func (s *stateManager) GetStorage(address crypto.Address, key binary.Word256) binary.Word256 {
	compKey := address.String() + hex.EncodeToString(key.Bytes())

	if val, ok := s.storageCache[compKey]; ok {
		return val
	}

	val, err := s.stub.GetState(compKey)

	if err != nil {
		s.PushError(err)

		return binary.Zero256
	}

	return binary.LeftPadWord256(val)
}

func (st *stateManager) GetBalance(address crypto.Address) uint64 {
	acc := st.account(address)
	if acc == nil {
		return 0
	}
	return acc.Balance
}

func (st *stateManager) GetPermissions(address crypto.Address) permission.AccountPermissions {
	acc := st.account(address)
	if acc == nil {
		return permission.AccountPermissions{}
	}
	return acc.Permissions
}

func (st *stateManager) GetCode(address crypto.Address) acm.Bytecode {
	acc := st.account(address)
	if acc == nil {
		return nil
	}
	return acc.Code
}

func (st *stateManager) Exists(address crypto.Address) bool {
	acc, err := st.GetAccount(address)

	if err != nil {
		st.PushError(err)
		return false
	}

	if acc == nil {
		return false
	}

	return true
}

func (st *stateManager) GetSequence(address crypto.Address) uint64 {
	acc := st.account(address)
	if acc == nil {
		return 0
	}
	return acc.Sequence
}

// Writer

func (st *stateManager) CreateAccount(address crypto.Address) {
	if st.Exists(address) {
		acc, _ := st.GetAccount(address)

		st.PushError(errors.ErrorCodef(errors.ErrorCodeDuplicateAddress,
			"tried to create an account at an address that already exists: %v %v", address, acc))
		return
	}

	st.updateAccount(&acm.Account{Address: address, Permissions: ContractPerms})
}

func (st *stateManager) InitCode(address crypto.Address, code []byte) {
	acc := st.mustAccount(address)
	if acc == nil {
		st.PushError(errors.ErrorCodef(errors.ErrorCodeInvalidAddress,
			"tried to initialise code for an account that does not exist: %v", address))
		return
	}
	if acc.Code != nil && acc.Code.Size() > 0 {
		st.PushError(errors.ErrorCodef(errors.ErrorCodeIllegalWrite,
			"tried to initialise code for a contract that already exists: %v existing code %v", address, acc.Code.Bytes()))
		return
	}
	acc.Code = code
	st.updateAccount(acc)
}

func (st *stateManager) RemoveAccount(address crypto.Address) {
	if !st.Exists(address) {
		st.PushError(errors.ErrorCodef(errors.ErrorCodeDuplicateAddress,
			"tried to remove an account at an address that does not exist: %v", address))
		return
	}

	err := st.removeAccount(address)

	if err != nil {
		st.PushError(err)
	}
}

func (s *stateManager) SetStorage(address crypto.Address, key, value binary.Word256) {
	var err error

	if err = s.stub.PutState(address.String()+hex.EncodeToString(key.Bytes()), value.Bytes()); err == nil {
		s.storageCache[address.String()+hex.EncodeToString(key.Bytes())] = value
	}

	if err != nil {
		s.PushError(err)
	}
}

func (st *stateManager) AddToBalance(address crypto.Address, amount uint64) {
	acc := st.mustAccount(address)
	if acc == nil {
		return
	}
	if binary.IsUint64SumOverflow(acc.Balance, amount) {
		st.PushError(errors.ErrorCodef(errors.ErrorCodeIntegerOverflow,
			"uint64 overflow: attempt to add %v to the balance of %s", amount, address))
		return
	}
	acc.Balance += amount

	st.updateAccount(acc)
}

func (st *stateManager) SubtractFromBalance(address crypto.Address, amount uint64) {
	acc := st.mustAccount(address)
	if acc == nil {
		return
	}
	if amount > acc.Balance {
		st.PushError(errors.ErrorCodef(errors.ErrorCodeInsufficientBalance,
			"insufficient funds: attempt to subtract %v from the balance of %s",
			amount, acc.Address))
		return
	}
	acc.Balance -= amount
	st.updateAccount(acc)
}

func (st *stateManager) SetPermission(address crypto.Address, permFlag permission.PermFlag, value bool) {
	acc := st.mustAccount(address)
	if acc == nil {
		return
	}
	acc.Permissions.Base.Set(permFlag, value)
	st.updateAccount(acc)
}

func (st *stateManager) UnsetPermission(address crypto.Address, permFlag permission.PermFlag) {
	acc := st.mustAccount(address)
	if acc == nil {
		return
	}
	acc.Permissions.Base.Unset(permFlag)
	st.updateAccount(acc)
}

func (st *stateManager) AddRole(address crypto.Address, role string) bool {
	acc := st.mustAccount(address)
	if acc == nil {
		return false
	}
	added := acc.Permissions.AddRole(role)
	st.updateAccount(acc)
	return added
}

func (st *stateManager) RemoveRole(address crypto.Address, role string) bool {
	acc := st.mustAccount(address)
	if acc == nil {
		return false
	}
	removed := acc.Permissions.RemoveRole(role)
	st.updateAccount(acc)
	return removed
}

func (st *stateManager) IncSequence(address crypto.Address) {
	acc := st.mustAccount(address)
	if acc == nil {
		return
	}
	acc.Sequence++
	st.updateAccount(acc)
}

///// ----------------------------------

func (s *stateManager) GetAccount(address crypto.Address) (*acm.Account, error) {
	var serializedAccount []byte
	var err error

	if val, ok := s.accountCache[address.String()]; ok {
		serializedAccount = val
	} else {
		serializedAccount, err = s.stub.GetState(address.String())

		if err != nil {
			return nil, err
		}
	}

	if len(serializedAccount) == 0 {
		return nil, nil
	}

	acct := acm.Account{
		Address: address,
	}

	err = acct.Unmarshal(serializedAccount)

	if err != nil {
		return nil, err
	}

	return &acct, nil
}

func (st *stateManager) account(address crypto.Address) *acm.Account {
	acc, err := st.GetAccount(address)
	if err != nil {
		st.PushError(err)
	}
	return acc
}

func (st *stateManager) updateAccount(updatedAccount *acm.Account) {
	serializedAccount, err := updatedAccount.Marshal()

	if err != nil {
		st.PushError(err)

		return
	}

	st.accountCache[updatedAccount.Address.String()] = serializedAccount

	err = st.stub.PutState(updatedAccount.Address.String(), serializedAccount)

	if err != nil {
		st.PushError(err)
	}
}

func (s *stateManager) removeAccount(address crypto.Address) error {
	if _, ok := s.accountCache[address.String()]; ok {
		delete(s.accountCache, address.String())
	}

	return s.stub.DelState(address.String())
}

func (st *stateManager) mustAccount(address crypto.Address) *acm.Account {
	acc := st.account(address)

	if acc == nil {
		st.PushError(errors.ErrorCodef(errors.ErrorCodeIllegalWrite,
			"attempted to modify non-existent account: %v", address))
	}

	return acc
}
