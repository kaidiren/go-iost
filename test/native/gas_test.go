package native

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/iost-official/go-iost/account"
	"github.com/iost-official/go-iost/common"
	"github.com/iost-official/go-iost/core/contract"
	"github.com/iost-official/go-iost/core/tx"
	"github.com/iost-official/go-iost/crypto"
	"github.com/iost-official/go-iost/db"
	"github.com/iost-official/go-iost/vm"
	"github.com/iost-official/go-iost/vm/database"
	"github.com/iost-official/go-iost/vm/host"
	"github.com/iost-official/go-iost/vm/native"
)

func toString(n int64) string {
	return strconv.FormatInt(n, 10)
}

func toIOSTFixed(n int64) *common.Fixed {
	return &common.Fixed{Value: n * native.IOSTRatio, Decimal: 8}
}

const initCoin int64 = 5000

var initCoinFN = toIOSTFixed(initCoin)

func gasTestInit() (*native.Impl, *host.Host, *contract.Contract, string, db.MVCCDB) {
	var tmpDB db.MVCCDB
	tmpDB, err := db.NewMVCCDB("mvcc")
	visitor := database.NewVisitor(100, tmpDB)
	if err != nil {
		panic(err)
	}
	context := host.NewContext(nil)
	context.Set("gas_price", int64(1))
	context.GSet("gas_limit", int64(100000))

	monitor := vm.NewMonitor()
	h := host.NewHost(context, visitor, monitor, nil)

	testAcc := "user1"
	user1 := getAccount(testAcc,"4nXuDJdU9MfP1TBY1W75o6ePDZNFuQ563YdkqVeEjW92aBcE6QDtFKPFWRBeKP8uMZcP7MGjfGubCLtu75t4ntxD")
	acc1, err := json.Marshal(user1)
	if err != nil {
		panic(err)
	}
	h.DB().MPut("iost.auth-account", testAcc, database.MustMarshal(string(acc1)))

	otherAcc := "user2"
	user2 := getAccount(otherAcc,"5oyBNyBeMFUKndGF8E3xkxmS3qugdYbwntSu8NEYtvC2DMmVcXgtmBqRxCLUCjxcu9zdcH3RkfKec3Q2xeiG48RL")
	acc2, err := json.Marshal(user2)
	if err != nil {
		panic(err)
	}
	h.DB().MPut("iost.auth-account", otherAcc, database.MustMarshal(string(acc2)))

	h.Context().Set("number", int64(1))
	h.Context().Set("time", int64(1541576370*1e9))
	h.Context().Set("stack_height", 0)
	h.Context().Set("publisher", testAcc)

	tokenContract := native.TokenABI()
	h.SetCode(tokenContract, "")

	authList := make(map[string]int)
	h.Context().Set("auth_contract_list", authList)
	authList[user1.Permissions["active"].Users[0].ID] = 2
	h.Context().Set("auth_list", authList)

	code := &contract.Contract{
		ID: "iost.gas",
	}

	e := &native.Impl{}
	e.Init()

	h.Context().Set("contract_name", "iost.token")
	h.Context().Set("abi_name", "abi")
	h.Context().GSet("receipts", []*tx.Receipt{})
	_, _, err = e.LoadAndCall(h, tokenContract, "create", "iost", testAcc, int64(initCoin), []byte("{}"))
	if err != nil {
		panic("create iost " + err.Error())
	}
	_, _, err = e.LoadAndCall(h, tokenContract, "issue", "iost", testAcc, fmt.Sprintf("%d", initCoin))
	if err != nil {
		panic("issue iost " + err.Error())
	}
	if initCoin*1e8 != visitor.TokenBalance("iost", testAcc) {
		panic("set initial coins failed " + strconv.FormatInt(visitor.TokenBalance("iost", testAcc), 10))
	}

	h.Context().Set("contract_name", "iost.gas")

	return e, h, code, testAcc, tmpDB
}

func timePass(h *host.Host, seconds int64) {
	h.Context().Set("time", h.Context().Value("time").(int64)+seconds*1e9)
}

func getAccount(name string, k string) *account.Account {
	key, err := account.NewKeyPair(common.Base58Decode(k), crypto.Ed25519)
	if err != nil {
		panic(err)
	}
	a := account.NewInitAccount(name, key.ID, key.ID)
	return a
}

func TestGas_NoPledge(t *testing.T) {
	Convey("test an account who did not pledge has 0 gas", t, func() {
		_, h, _, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		gas, _ := h.GasManager.CurrentGas(testAcc)
		So(gas.Value, ShouldEqual, 0)
	})
}

func TestGas_PledgeAuth(t *testing.T) {
	Convey("test pledging requires auth", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		pledgeAmount := toIOSTFixed(200)
		authList := make(map[string]int)
		h.Context().Set("auth_list", authList)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, testAcc, pledgeAmount.ToString())
		So(err, ShouldNotBeNil)
	})
}

func TestGas_NotEnoughMoney(t *testing.T) {
	Convey("test pledging with not enough money", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		pledgeAmount := toIOSTFixed(20000)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, testAcc, pledgeAmount.ToString())
		So(err, ShouldNotBeNil)
	})
}

func TestGas_Pledge(t *testing.T) {
	Convey("test pledge", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		pledgeAmount := toIOSTFixed(200)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, testAcc, pledgeAmount.ToString())
		So(err, ShouldBeNil)
		So(h.DB().TokenBalance("iost", testAcc), ShouldEqual, initCoinFN.Value - pledgeAmount.Value)
		So(h.DB().TokenBalance("iost", "iost.gas"), ShouldEqual, pledgeAmount.Value)
		Convey("After pledge, you will get some gas immediately", func() {
			gas, _ := h.GasManager.CurrentGas(testAcc)
			gasEstimated := pledgeAmount.Multiply(native.GasImmediateReward)
			So(gas.Equals(gasEstimated), ShouldBeTrue)
		})
		Convey("Then gas increases at a predefined rate", func() {
			delta := int64(5)
			timePass(h, delta)
			gas, _ := h.GasManager.CurrentGas(testAcc)
			gasEstimated := pledgeAmount.Multiply(native.GasImmediateReward).Add(pledgeAmount.Multiply(native.GasIncreaseRate).Times(delta))
			So(gas.Equals(gasEstimated), ShouldBeTrue)
		})
		Convey("Then gas will reach limit and not increase any longer", func() {
			delta := int64(native.GasFulfillSeconds + 4000)
			timePass(h, delta)
			gas, _ := h.GasManager.CurrentGas(testAcc)
			gasEstimated := pledgeAmount.Multiply(native.GasLimit)
			So(gas.Equals(gasEstimated), ShouldBeTrue)
		})
	})
}

func TestGas_PledgeMore(t *testing.T) {
	Convey("test you can pledge more after first time pledge", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		firstTimePledgeAmount := toIOSTFixed(200)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, testAcc, firstTimePledgeAmount.ToString())
		So(err, ShouldBeNil)
		delta1 := int64(5)
		timePass(h, delta1)
		gasBeforeSecondPledge, _ := h.GasManager.CurrentGas(testAcc)
		secondTimePledgeAmount := toIOSTFixed(300)
		_, _, err = e.LoadAndCall(h, code, "pledge", testAcc, testAcc, secondTimePledgeAmount.ToString())
		So(err, ShouldBeNil)
		delta2 := int64(10)
		timePass(h, delta2)
		gasAfterSecondPledge, _ := h.GasManager.CurrentGas(testAcc)
		gasEstimated := gasBeforeSecondPledge.Add(secondTimePledgeAmount.Multiply(native.GasImmediateReward).Add(
			secondTimePledgeAmount.Add(firstTimePledgeAmount).Multiply(native.GasIncreaseRate).Times(delta2)))
		So(gasAfterSecondPledge.Equals(gasEstimated), ShouldBeTrue)
		So(h.DB().TokenBalance("iost", testAcc), ShouldEqual, initCoinFN.Sub(firstTimePledgeAmount).Sub(secondTimePledgeAmount).Value)
		So(h.DB().TokenBalance("iost", "iost.gas"), ShouldEqual,firstTimePledgeAmount.Add(secondTimePledgeAmount).Value)
	})
}

func TestGas_UseGas(t *testing.T) {
	Convey("test using gas", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		pledgeAmount := int64(200)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, testAcc, toString(pledgeAmount))
		So(err, ShouldBeNil)
		delta1 := int64(5)
		timePass(h, delta1)
		gasBeforeUse, _ := h.GasManager.CurrentGas(testAcc)
		gasCost := toIOSTFixed(100)
		_, err = h.GasManager.CostGas(testAcc, gasCost)
		So(err, ShouldBeNil)
		gasAfterUse, _ := h.GasManager.CurrentGas(testAcc)
		gasEstimated := gasBeforeUse.Sub(gasCost)
		So(gasAfterUse.Equals(gasEstimated), ShouldBeTrue)
	})
}

func TestGas_unpledge(t *testing.T) {
	Convey("test unpledge", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		pledgeAmount := toIOSTFixed(200)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, testAcc, pledgeAmount.ToString())
		So(err, ShouldBeNil)
		delta1 := int64(10)
		timePass(h, delta1)
		unpledgeAmount := toIOSTFixed(190)
		balanceBeforeunpledge := h.DB().TokenBalance("iost", testAcc)
		_, _, err = e.LoadAndCall(h, code, "unpledge", testAcc, testAcc, unpledgeAmount.ToString())
		So(err, ShouldBeNil)
		So(h.DB().TokenBalance("iost", testAcc), ShouldEqual, balanceBeforeunpledge)
		So(h.DB().TokenBalance("iost", "iost.gas"), ShouldEqual, pledgeAmount.Sub(unpledgeAmount).Value)
		gas, _ := h.GasManager.CurrentGas(testAcc)
		Convey("After unpledging, the gas limit will decrease. If current gas is more than the new limit, it will be decrease.", func() {
			gasEstimated := pledgeAmount.Sub(unpledgeAmount).Multiply(native.GasLimit)
			So(gas.Equals(gasEstimated), ShouldBeTrue)
		})
		Convey("after 3 days, the frozen money is available", func() {
			timePass(h, native.UnpledgeFreezeSeconds)
			h.Context().Set("contract_name", "iost.token")
			rs, _, err := e.LoadAndCall(h, native.TokenABI(), "balanceOf", "iost", testAcc)
			So(err, ShouldBeNil)
			expected := initCoinFN.Sub(pledgeAmount).Add(unpledgeAmount)
			So(rs[0], ShouldEqual, expected.ToString())
			So(h.DB().TokenBalance("iost", testAcc), ShouldEqual, expected.Value)
		})
	})
}

func TestGas_unpledgeTooMuch(t *testing.T) {
	Convey("test unpledge too much: each account has a minimum pledge", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		pledgeAmount := int64(200)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, testAcc, toString(pledgeAmount))
		So(err, ShouldBeNil)
		delta1 := int64(1)
		timePass(h, delta1)
		unpledgeAmount := (pledgeAmount - native.GasMinPledgeInIOST) + int64(1)
		_, _, err = e.LoadAndCall(h, code, "unpledge", testAcc, testAcc, toString(unpledgeAmount))
		So(err, ShouldNotBeNil)
	})
}

func TestGas_PledgeunpledgeForOther(t *testing.T) {
	Convey("test pledge for others", t, func() {
		e, h, code, testAcc, tmpDB := gasTestInit()
		defer func() {
			tmpDB.Close()
			os.RemoveAll("mvcc")
		}()
		otherAcc := "user2"
		pledgeAmount := toIOSTFixed(200)
		_, _, err := e.LoadAndCall(h, code, "pledge", testAcc, otherAcc, pledgeAmount.ToString())
		So(err, ShouldBeNil)
		So(h.DB().TokenBalance("iost", testAcc), ShouldEqual, initCoinFN.Value - pledgeAmount.Value)
		So(h.DB().TokenBalance("iost", "iost.gas"), ShouldEqual, pledgeAmount.Value)
		Convey("After pledge, you will get some gas immediately", func() {
			gas, _ := h.GasManager.CurrentGas(otherAcc)
			gasEstimated := pledgeAmount.Multiply(native.GasImmediateReward)
			So(gas.Equals(gasEstimated), ShouldBeTrue)
		})
		Convey("If one pledge for others, he will get no gas himself", func() {
			gas, _ := h.GasManager.CurrentGas(testAcc)
			So(gas.Value, ShouldBeZeroValue)
		})
		Convey("Test unpledge for others", func() {
			unpledgeAmount := toIOSTFixed(190)
			_, _, err = e.LoadAndCall(h, code, "unpledge", testAcc, otherAcc, unpledgeAmount.ToString())
			So(err, ShouldBeNil)
			timePass(h, native.UnpledgeFreezeSeconds)
			h.Context().Set("contract_name", "iost.token")
			rs, _, err := e.LoadAndCall(h, native.TokenABI(), "balanceOf", "iost", testAcc)
			So(err, ShouldBeNil)
			expected := initCoinFN.Sub(pledgeAmount).Add(unpledgeAmount)
			So(rs[0], ShouldEqual, expected.ToString())
		})
	})
}
