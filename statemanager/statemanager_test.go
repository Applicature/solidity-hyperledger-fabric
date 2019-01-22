/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statemanager_test

import (
	"encoding/hex"
	"errors"
	"github.com/hyperledger/burrow/acm"
	"github.com/hyperledger/burrow/binary"
	"github.com/hyperledger/burrow/crypto"

	"github.com/hyperledger/fabric-chaincode-evm/mocks/evmcc"
	"github.com/hyperledger/fabric-chaincode-evm/statemanager"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Statemanager", func() {

	var (
		sm            statemanager.StateManager
		mockStub      *evmcc.MockStub
		addr          crypto.Address
		fakeGetLedger map[string][]byte
		fakePutLedger map[string][]byte
	)

	BeforeEach(func() {
		mockStub = &evmcc.MockStub{}
		sm = statemanager.NewStateManager(mockStub)

		var err error
		addr, err = crypto.AddressFromBytes([]byte("0000000000000address"))
		Expect(err).ToNot(HaveOccurred())
		fakeGetLedger = make(map[string][]byte)
		fakePutLedger = make(map[string][]byte)

		//Writing to a separate ledger so that writes to the ledger cannot be read in the same transaction.
		// This is more consistent with the behavior fo the ledger
		mockStub.PutStateStub = func(key string, value []byte) error {
			fakePutLedger[key] = value

			return nil
		}

		mockStub.GetStateStub = func(key string) ([]byte, error) {
			return fakeGetLedger[key], nil
		}

		mockStub.DelStateStub = func(key string) error {
			delete(fakePutLedger, key)
			return nil
		}
	})

	Describe("GetAccount", func() {
		It("returns the account associated with the address", func() {
			expectedAcct := acm.Account{
				Address:     addr,
				Code:        []byte("account code"),
				Permissions: statemanager.ContractPerms,
				PublicKey:   crypto.PublicKey{},
			}

			serializedAccount, err := expectedAcct.Marshal()

			fakeGetLedger[addr.String()] = serializedAccount

			secondAccount := acm.Account{}

			err = secondAccount.Unmarshal(serializedAccount)

			Expect(err).ToNot(HaveOccurred())

			acct, err := sm.GetAccount(addr)
			Expect(err).ToNot(HaveOccurred())

			Expect(*acct).To(Equal(secondAccount))
		})

		Context("when no account exists", func() {
			It("returns an empty account", func() {
				acct, err := sm.GetAccount(addr)
				Expect(err).ToNot(HaveOccurred())

				var nulledAccount *acm.Account = nil

				Expect(acct).To(Equal(nulledAccount))
			})
		})

		Context("when GetState errors out", func() {
			BeforeEach(func() {
				mockStub.GetStateReturns(nil, errors.New("boom!"))
			})

			It("returns an empty account and an error", func() {
				acct, err := sm.GetAccount(addr)
				Expect(err).To(HaveOccurred())

				Expect(acct).To(BeNil())
			})
		})
	})

	Describe("GetStorage", func() {
		var expectedVal, key binary.Word256
		BeforeEach(func() {
			expectedVal = binary.LeftPadWord256([]byte("storage-value"))
			key = binary.LeftPadWord256([]byte("key"))
		})

		It("returns the value associated with the key", func() {
			fakeGetLedger[addr.String()+hex.EncodeToString(key.Bytes())] = expectedVal.Bytes()

			val := sm.GetStorage(addr, key)
			Expect(sm.Error()).ToNot(HaveOccurred())

			Expect(val).To(Equal(expectedVal))
		})

		Context("when GetState returns an error", func() {
			BeforeEach(func() {
				mockStub.GetStateReturns(nil, errors.New("boom!"))
			})

			It("returns an error", func() {
				val := sm.GetStorage(addr, key)

				Expect(sm.Error()).To(HaveOccurred())

				Expect(val).To(Equal(binary.Word256{}))
			})
		})

		Context("when a GetStorage is called after an SetStorage on the same key in the same tx", func() {
			var initialVal, updatedVal binary.Word256
			BeforeEach(func() {
				initialVal = binary.LeftPadWord256([]byte("storage-value"))
				updatedVal = binary.LeftPadWord256([]byte("updated-storage-value"))

				fakeGetLedger[addr.String()+hex.EncodeToString(key.Bytes())] = initialVal.Bytes()

				val := sm.GetStorage(addr, key)
				Expect(sm.Error()).ToNot(HaveOccurred())
				Expect(val).To(Equal(initialVal))

				sm.SetStorage(addr, key, updatedVal)
				Expect(sm.Error()).ToNot(HaveOccurred())
			})

			It("returns the account that was previously written in the same tx", func() {
				val := sm.GetStorage(addr, key)
				Expect(sm.Error()).ToNot(HaveOccurred())
				Expect(val).To(Equal(updatedVal))
			})
		})
	})

	Describe("GetBalance", func() {
		Context("when account not exists", func() {
			It("returns zero", func() {
				balance := sm.GetBalance(addr)
				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(balance).To(Equal(uint64(0)))
			})
		})

		Context("when account exists", func() {
			It("with unset balance", func() {
				account := acm.Account{Address: addr}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				balance := sm.GetBalance(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(balance).To(Equal(uint64(0)))
			})

			It("with set balance", func() {
				account := acm.Account{Address: addr, Balance: uint64(10002123123)}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				balance := sm.GetBalance(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(balance).To(Equal(uint64(10002123123)))
			})
		})
	})

	Describe("Exists", func() {
		Context("when account not exists", func() {
			It("returns error", func() {
				isExists := sm.Exists(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(isExists).To(Equal(false))
			})
		})

		Context("when account exists", func() {
			It("with unset balance", func() {
				account := acm.Account{Address: addr}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				isExists := sm.Exists(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(isExists).To(Equal(true))
			})
		})
	})

	Describe("GetCode", func() {
		Context("when account not exists", func() {
			It("returns nil", func() {
				code := sm.GetCode(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(code).To(BeNil())
			})
		})

		Context("when account exists", func() {
			It("with unset code", func() {
				account := acm.Account{Address: addr}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				code := sm.GetCode(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(code).To(Equal(acm.Bytecode{}))
			})

			It("with set code", func() {
				account := acm.Account{Address: addr, Code: []byte("account code")}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				code := sm.GetCode(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(code).To(BeEquivalentTo("account code"))
			})
		})
	})

	Describe("CreateAccount", func() {
		var initialCode []byte
		BeforeEach(func() {
			account := acm.Account{Code: []byte("account code")}

			serializedAccount, _ := account.Marshal()

			initialCode = serializedAccount
		})

		Context("when the account didn't exist", func() {
			It("creates the account", func() {
				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				sm.CreateAccount(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.Exists(addr)).To(Equal(true))

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				account, _ := sm.GetAccount(addr)

				serializedAccount, _ := account.Marshal()

				key, code := mockStub.PutStateArgsForCall(0)

				Expect(key).To(Equal(addr.String()))
				Expect(code).To(Equal(serializedAccount))
			})
		})

		Context("when the account exists", func() {
			It("create the account", func() {
				account := acm.Account{Address: addr, Code: initialCode}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				sm.CreateAccount(addr)

				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				Expect(sm.Error()).To(HaveOccurred())
			})
		})

		Context("when stub throws an error", func() {
			BeforeEach(func() {
				mockStub.PutStateReturns(errors.New("boom!"))
			})

			It("returns an error", func() {
				sm.CreateAccount(addr)

				Expect(sm.Error()).To(HaveOccurred())
			})
		})
	})

	Describe("InitCode", func() {
		var initialCode []byte
		BeforeEach(func() {
			account := acm.Account{Code: []byte("account code")}

			serializedAccount, _ := account.Marshal()

			initialCode = serializedAccount
		})

		Context("when account just created", func() {
			It("successfully init code", func() {
				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				sm.CreateAccount(addr)

				Expect(sm.Error()).ToNot(HaveOccurred())
				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.Exists(addr)).To(Equal(true))

				account, _ := sm.GetAccount(addr)

				sm.InitCode(addr, initialCode)
				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(2))

				account.Code = initialCode

				serializedAccount, _ := account.Marshal()

				key, code := mockStub.PutStateArgsForCall(1)

				Expect(key).To(Equal(addr.String()))
				Expect(code).To(Equal(serializedAccount))
			})
		})

		Context("when the account exists without Code specified", func() {
			It("return errro", func() {
				account := acm.Account{Address: addr}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				sm.InitCode(addr, initialCode)

				Expect(sm.Error()).ToNot(HaveOccurred())
			})
		})

		Context("when the account exists with code specified", func() {
			It("return errro", func() {
				account := acm.Account{Address: addr, Code: initialCode}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				sm.InitCode(addr, initialCode)

				Expect(sm.Error()).To(HaveOccurred())
			})
		})

		Context("when the account exists with code specified", func() {
			It("return errro", func() {
				account := acm.Account{Address: addr, Code: initialCode}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				sm.InitCode(addr, initialCode)

				Expect(sm.Error()).To(HaveOccurred())
			})
		})
	})

	Describe("RemoveAccount", func() {
		Context("when the account existed previously", func() {
			It("removes the account", func() {
				account := acm.Account{}

				serializedAccount, _ := account.Marshal()

				fakeGetLedger[addr.String()] = serializedAccount

				sm.RemoveAccount(addr)
				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(mockStub.DelStateCallCount()).To(Equal(1))
				delAddr := mockStub.DelStateArgsForCall(0)
				Expect(delAddr).To(Equal(addr.String()))
			})
		})

		Context("when the account did not exists previously", func() {
			It("returns an error", func() {
				sm.RemoveAccount(addr)
				Expect(sm.Error()).To(HaveOccurred())

				Expect(mockStub.DelStateCallCount()).To(Equal(0))
			})
		})

		Context("when stub throws an error", func() {
			BeforeEach(func() {
				mockStub.DelStateReturns(errors.New("boom!"))
			})

			It("returns an error", func() {
				sm.RemoveAccount(addr)
				Expect(sm.Error()).To(HaveOccurred())
			})
		})
	})

	Describe("SetStorage", func() {
		var (
			key, initialVal binary.Word256
			compKey         string
		)

		BeforeEach(func() {

			initialVal = binary.LeftPadWord256([]byte("storage-value"))
			key = binary.LeftPadWord256([]byte("key"))
			compKey = addr.String() + hex.EncodeToString(key.Bytes())
		})

		Context("when key already exists", func() {
			It("updates the key value pair", func() {
				err := mockStub.PutState(compKey, initialVal.Bytes())
				Expect(err).ToNot(HaveOccurred())

				updatedVal := binary.LeftPadWord256([]byte("updated-storage-value"))

				sm.SetStorage(addr, key, updatedVal)
				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(2))
				putKey, putVal := mockStub.PutStateArgsForCall(1)
				Expect(putKey).To(Equal(compKey))
				Expect(putVal).To(Equal(updatedVal.Bytes()))
			})
		})

		Context("when the key does not exist", func() {
			It("creates the key value pair", func() {
				sm.SetStorage(addr, key, initialVal)
				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(1))
				putKey, putVal := mockStub.PutStateArgsForCall(0)
				Expect(putKey).To(Equal(compKey))
				Expect(putVal).To(Equal(initialVal.Bytes()))
			})
		})

		Context("when stub throws an error", func() {
			BeforeEach(func() {
				mockStub.PutStateReturns(errors.New("boom!"))
			})

			It("returns an error", func() {
				sm.SetStorage(addr, key, initialVal)
				Expect(sm.Error()).To(HaveOccurred())

				val, err := mockStub.GetState(compKey)
				Expect(err).ToNot(HaveOccurred())
				Expect(val).To(BeEmpty())
			})
		})
	})

	Describe("AddToBalance", func() {
		Context("for new account", func() {
			It("first time call of AddToBalance", func() {
				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				sm.CreateAccount(addr)

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.GetBalance(addr)).To(Equal(uint64(0)))

				sm.AddToBalance(addr, 102345)

				Expect(mockStub.PutStateCallCount()).To(Equal(2))

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.GetBalance(addr)).To(Equal(uint64(102345)))
			})

			It("second time call of AddToBalance", func() {
				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				sm.CreateAccount(addr)

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.GetBalance(addr)).To(Equal(uint64(0)))

				sm.AddToBalance(addr, 102345)

				Expect(mockStub.PutStateCallCount()).To(Equal(2))

				sm.AddToBalance(addr, 102)

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(mockStub.PutStateCallCount()).To(Equal(3))

				Expect(sm.GetBalance(addr)).To(Equal(uint64(102345  + 102)))
			})
		})

		Context("when the account exists", func() {
			It("first time call of AddToBalance", func() {
				account := acm.Account{Address: addr, Balance: 123}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123)))

				sm.AddToBalance(addr, 102345)

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123 + 102345)))
			})

			It("second time call of AddToBalance", func() {
				account := acm.Account{Address: addr, Balance: 123}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123)))

				sm.AddToBalance(addr, 102345)

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				sm.AddToBalance(addr, 102)

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(mockStub.PutStateCallCount()).To(Equal(2))

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123 + 102345 + 102)))
			})
		})
	})

	Describe("SubtractFromBalance", func() {
		Context("for new account", func() {
			It("first time call of AddToBalance", func() {
				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				sm.CreateAccount(addr)

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.GetBalance(addr)).To(Equal(uint64(0)))

				sm.SubtractFromBalance(addr, 102345)

				Expect(sm.Error()).To(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				Expect(sm.GetBalance(addr)).To(Equal(uint64(0)))
			})

			It("second time call of AddToBalance", func() {
				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				sm.CreateAccount(addr)

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.GetBalance(addr)).To(Equal(uint64(0)))

				sm.SubtractFromBalance(addr, 102345)

				Expect(sm.Error()).To(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				sm.SubtractFromBalance(addr, 102)

				Expect(sm.Error()).To(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				Expect(sm.GetBalance(addr)).To(Equal(uint64(0)))
			})
		})

		Context("when the account exists", func() {
			It("first time call of AddToBalance", func() {
				account := acm.Account{Address: addr, Balance: 123}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				Expect(mockStub.PutStateCallCount()).To(Equal(0))

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123)))

				sm.SubtractFromBalance(addr, 1)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123 - 1)))
			})

			It("second time call of AddToBalance", func() {
				account := acm.Account{Address: addr, Balance: 123}

				fakeGetLedger[addr.String()], _ = account.Marshal()

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123)))

				sm.SubtractFromBalance(addr, 10)

				Expect(sm.Error()).ToNot(HaveOccurred())

				Expect(mockStub.PutStateCallCount()).To(Equal(1))

				sm.SubtractFromBalance(addr, 102)

				Expect(sm.Error()).ToNot(HaveOccurred())

				fakeGetLedger[addr.String()] = fakePutLedger[addr.String()]

				Expect(mockStub.PutStateCallCount()).To(Equal(2))

				Expect(sm.GetBalance(addr)).To(Equal(uint64(123 - 10 - 102)))
			})
		})
	})
})
