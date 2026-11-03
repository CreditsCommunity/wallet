#!/usr/bin/env python3
"""A stand-in for a node's API that checks the transfers the sample clients send, offline.

Copied from the community node, CreditsCommunity/node samples/clients/testdata/fake_node.py
(AGPL-3.0), with a few calls added for the wallet (marked "wallet:"): SyncStateGet, PoolListGet,
PoolInfoGet, ActualFeeGet, WalletBalanceGet, TransactionsGet, and WalletTransactionsCountGet
following the accepted transfers. --idl defaults to this repository's submodule.

wallet (milestone 2): transfers with a user field are checked too: a text (user data, user field
1, signed as u32 size + bytes) or the delegated field (5, signed as u64), the way ci/testnet/fund.py
builds them; delegations are recorded and listed by WalletDataGet (a withdrawal needs as much
delegated without a time limit, every amount at least 5 CS). A token, FakeToken (FTK, 2 decimals),
is deployed at TOKEN with methods transfer(String, BigDecimal), balanceOf(String), getDecimal(),
getSymbol() and getName(); its signed transfer calls move the balances, and TokenBalancesGet,
TokenInfoGet and TokenWalletTransfersGet report them, unless --no-token-cache, where they answer as
a node without [api] tokens_cache. Every TransactionsGet is printed ("HISTORY ..."), so that a test
sees which node served the history.

  fake_node.py [--port 19090] [--idl DIR] [--no-token-cache]

It answers WalletTransactionsCountGet (last inner id 6, so the next transfer has inner id 7) and
TransactionFlow, which accepts a transfer only when its fields are those of a CS transfer and its
ed25519 signature is valid over csdb::Transaction::to_byte_stream_for_sig() rebuilt from the
fields the client sent: what a real node checks first. Every transfer is printed. Nothing is
forwarded anywhere. Used by .github/workflows/samples.yml; run it to try `transfer` safely.

For the contract commands it also plays a node with one made-up contract, FakeContract:
SmartContractCompile returns fixed byte code; a deploy is accepted when its target is the address
the node derives (blake2s of deployer, inner id, byte code) and its signature is valid over the
invocation serialized as the node serializes it (every non-optional field written, so a client that
leaves one out fails here as it would on a node); then SmartContractGet and ContractAllMethodsGet
describe it, and calls and getters are accepted only with the Variant types its methods declare:
  int add(int n)                  returns the sum of every n added so far
  void setNote(String text)       returns void
  BigDecimal paidBy(String addr)  returns 12.50
  boolean isOpen()                returns true
"""
import argparse
import hashlib
import os
import struct
import time
from decimal import Decimal

import base58
import nacl.exceptions
import nacl.signing
import thriftpy2
from thriftpy2.protocol import TBinaryProtocolFactory
from thriftpy2.rpc import make_server
from thriftpy2.transport import TBufferedTransportFactory
from thriftpy2.utils import serialize

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_IDL = os.path.join(HERE, "..", "third-party", "thrift-interface-definitions")  # wallet: path
LAST_INNER_ID = 6
BYTE_CODE = b"\xca\xfe\xba\xbe fake byte code of FakeContract"
# name, argument types, return type
METHODS = [("add", ["int"], "int"), ("setNote", ["java.lang.String"], "void"),
           ("paidBy", ["java.lang.String"], "java.math.BigDecimal"), ("isOpen", [], "boolean")]
VARIANT_OF = {"int": "v_int", "java.lang.String": "v_string", "java.math.BigDecimal": "v_big_decimal"}
# wallet: the fake token
TOKEN = base58.b58decode("9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd")
TOKEN_CODE = b"\xca\xfe\xba\xbe fake byte code of FakeToken"
TOKEN_METHODS = [("transfer", ["java.lang.String", "java.math.BigDecimal"], "boolean"),
                 ("balanceOf", ["java.lang.String"], "java.math.BigDecimal"), ("getDecimal", [], "int"),
                 ("getSymbol", [], "java.lang.String"), ("getName", [], "java.lang.String")]
TEST_HOLDER = "FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF"  # testdata/test-key.txt


def with_defaults(sci):
    """The invocation as the node serializes it again: every field the IDL does not mark optional
    is written, with its default value when the client left it out."""
    for name, default in (("method", ""), ("params", []), ("usedContracts", []), ("forgetNewState", False),
                          ("version", 1)):
        if getattr(sci, name) is None:
            setattr(sci, name, default)
    d = sci.smartContractDeploy
    if d is not None:
        for name, default in (("sourceCode", ""), ("byteCodeObjects", []), ("hashState", ""), ("tokenStandard", 0),
                              ("lang", 0)):
            if getattr(d, name) is None:
                setattr(d, name, default)
    return sci


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=19090)
    parser.add_argument("--idl", default=DEFAULT_IDL)
    parser.add_argument("--no-token-cache", action="store_true")
    args = parser.parse_args()
    idl = os.path.abspath(args.idl)
    api = thriftpy2.load(os.path.join(idl, "api.thrift"), module_name="api_thrift", include_dirs=[idl])
    general = api.general

    def ok():
        return general.APIResponse(code=0, message="")

    def refuse(why):
        print(f"REFUSED: {why}", flush=True)
        return api.TransactionFlowResult(status=general.APIResponse(code=1, message=why))

    deployed = set()
    counter = [0]
    last_inner = [LAST_INNER_ID]  # wallet: the last inner id moves on with the accepted transactions
    head = [1000]
    accepted = []
    delegations = []  # wallet: [source, target, Decimal amount, valid_until (0: no limit)]
    token_balances = {TEST_HOLDER: Decimal("1000.00")}
    token_transfers = []

    def flow_ok(fee_fraction=8_740_000_000_000_000, result=None):
        return api.TransactionFlowResult(status=ok(), roundNum=1, smart_contract_result=result,
                                         id=api.TransactionId(poolSeq=head[0], index=0),
                                         fee=general.Amount(integral=0, fraction=fee_fraction))

    def token_method(t, sci, getter):
        # wallet: FakeToken, typed as a real executor would check it
        known = {m[0]: m for m in TOKEN_METHODS}
        if sci.method not in known:
            return refuse(f"FakeToken has no method {sci.method}")
        _, types, _ = known[sci.method]
        params = sci.params or []
        if len(params) != len(types):
            return refuse(f"{sci.method} takes {len(types)} arguments, got {len(params)}")
        for declared, p in zip(types, params):
            if getattr(p, VARIANT_OF[declared]) is None:
                return refuse(f"{sci.method}: a {declared} argument must be a {VARIANT_OF[declared]}, got {p}")
        caller = base58.b58encode(t.source).decode()
        if sci.method == "getDecimal":
            result = general.Variant(v_int=2)
        elif sci.method == "getSymbol":
            result = general.Variant(v_string="FTK")
        elif sci.method == "getName":
            result = general.Variant(v_string="Fake Token")
        elif sci.method == "balanceOf":
            result = general.Variant(v_big_decimal=str(token_balances.get(params[0].v_string, Decimal("0.00"))))
        else:
            to, value = params[0].v_string, Decimal(params[1].v_big_decimal)
            if value <= 0 or value != value.quantize(Decimal("0.01")) or token_balances.get(caller, 0) < value:
                return refuse(f"call to contract failed: transfer of {value} from {caller}")
            if not getter:
                token_balances[caller] -= value
                token_balances[to] = token_balances.get(to, Decimal("0.00")) + value
                head[0] += 1
                token_transfers.append(api.TokenTransfer(
                    token=TOKEN, code="FTK", sender=t.source, receiver=base58.b58decode(to), amount=str(value),
                    initiator=t.source, transaction=api.TransactionId(poolSeq=head[0], index=0),
                    time=int(time.time() * 1000), state=1, fee=api.AmountCommission(commission=17279), extraFee=[]))
                t.type = api.TransactionType.TT_TokenTransfer
                accepted.append(api.SealedTransaction(id=api.TransactionId(poolSeq=head[0], index=0), trxn=t))
            result = general.Variant(v_boolean=True)
        shown = ", ".join(f"{k}={v!r}" for p in params for k, v in vars(p).items() if v is not None)
        print(f"ACCEPTED: {'getter' if getter else 'token call'} {sci.method}({shown})", flush=True)
        return flow_ok(0 if getter else 17_480_000_000_000_000, result)

    def no_cache(result):
        return result(status=general.APIResponse(
            code=1, message="This node doesn't provide such info (the token cache is off: [api] tokens_cache=true)"))

    def contract_flow(t):
        sci = t.smartContract
        if t.type == api.TransactionType.TT_ContractCall and sci.forgetNewState:
            # a getter: nothing is signed or sent
            if t.target == TOKEN:
                return token_method(t, sci, getter=True)
            if t.target not in deployed:
                return refuse("getter on an unknown contract")
            return run_method(sci, getter=True)
        if t.type not in (api.TransactionType.TT_ContractDeploy, api.TransactionType.TT_ContractCall) \
                or t.currency != 1 or t.id != last_inner[0] + 1 or t.fee is None or t.balance is None:
            return refuse(f"not a well formed contract transaction: type {t.type}, inner id {t.id}")
        if t.amount.integral != 0 or t.amount.fraction != 0:
            return refuse("a contract transaction with an amount")
        code = serialize(with_defaults(sci), TBinaryProtocolFactory())
        signed = b"".join([
            t.id.to_bytes(6, "little"), t.source, t.target, struct.pack("<iQ", 0, 0),
            struct.pack("<H", t.fee.commission & 0xFFFF), struct.pack("<B", t.currency),
            struct.pack("<BI", 1, len(code)) + code,
        ])
        try:
            nacl.signing.VerifyKey(t.source).verify(signed, t.signature)
        except (nacl.exceptions.BadSignatureError, ValueError) as e:
            return refuse(f"bad signature over the contract invocation: {e}")
        if t.type == api.TransactionType.TT_ContractDeploy:
            d = sci.smartContractDeploy
            if d is None or sci.method or [b.byteCode for b in d.byteCodeObjects] != [BYTE_CODE] or not d.sourceCode:
                return refuse("a deploy without the compiled byte code and the source")
            expected = hashlib.blake2s(t.source + t.id.to_bytes(6, "little") + BYTE_CODE).digest()
            if t.target != expected:
                return refuse(f"deploy to {base58.b58encode(t.target).decode()}, the contract address is "
                              f"{base58.b58encode(expected).decode()}")
            deployed.add(t.target)
            print(f"ACCEPTED: deploy of {base58.b58encode(t.target).decode()} by {base58.b58encode(t.source).decode()}",
                  flush=True)
            return api.TransactionFlowResult(status=ok(), roundNum=1, id=api.TransactionId(poolSeq=1, index=0),
                                             fee=general.Amount(integral=0, fraction=8_740_000_000_000_000))
        last_inner[0] = t.id  # wallet: in a block
        if t.target == TOKEN:
            return token_method(t, sci, getter=False)
        if t.target not in deployed:
            return refuse("call to an unknown contract")
        return run_method(sci, getter=False)

    def run_method(sci, getter):
        known = {m[0]: m for m in METHODS}
        if sci.method not in known:
            return refuse(f"no method {sci.method}")
        _, types, _ = known[sci.method]
        params = sci.params or []
        if len(params) != len(types):
            return refuse(f"{sci.method} takes {len(types)} arguments, got {len(params)}")
        for declared, p in zip(types, params):
            if getattr(p, VARIANT_OF[declared]) is None:
                return refuse(f"{sci.method}: a {declared} argument must be a {VARIANT_OF[declared]}, got {p}")
        if sci.method == "add":
            counter[0] += params[0].v_int
            result = general.Variant(v_int=counter[0])
        elif sci.method == "setNote":
            result = general.Variant(v_void=0)
        elif sci.method == "paidBy":
            result = general.Variant(v_big_decimal="12.50")
        else:
            result = general.Variant(v_boolean=True)
        shown = ", ".join(f"{k}={v!r}" for p in params for k, v in vars(p).items() if v is not None)
        print(f"ACCEPTED: {'getter' if getter else 'call'} {sci.method}({shown})", flush=True)
        return api.TransactionFlowResult(status=ok(), smart_contract_result=result, roundNum=1,
                                         id=api.TransactionId(poolSeq=1, index=0),
                                         fee=general.Amount(integral=0, fraction=0 if getter else 8_740_000_000_000_000))


    class Handler:
        def WalletTransactionsCountGet(self, address):
            return api.WalletTransactionsCountGetResult(status=ok(), lastTransactionInnerId=last_inner[0])

        # wallet: the calls the wallet makes before sending and to show balances and history
        def SyncStateGet(self):
            head[0] += 1
            return api.SyncStateResult(status=ok(), currRound=head[0] + 1, lastBlock=head[0])

        def PoolListGet(self, offset, limit):
            pool = api.Pool(hash=b"\x00" * 32, prevHash=b"\x00" * 32, time=int(time.time() * 1000), transactionsCount=0,
                            poolNumber=head[0], writer=b"\x00" * 32, totalFee=general.Amount(integral=0, fraction=0),
                            confidants=[])
            return api.PoolListGetResult(status=ok(), result=True, count=1, pools=[pool])

        def PoolInfoGet(self, sequence, index):
            # a pruned node: it holds blocks from 900 on
            return api.PoolInfoGetResult(status=ok(), isFound=900 <= sequence <= head[0])

        def ActualFeeGet(self, size):
            return api.ActualFeeGetResult(fee=api.AmountCommission(commission=17279))

        def WalletBalanceGet(self, address):
            return api.WalletBalanceGetResult(status=ok(), balance=general.Amount(integral=100, fraction=0))

        def TransactionsGet(self, address, offset, limit):
            print(f"HISTORY TransactionsGet {base58.b58encode(address).decode()}", flush=True)  # wallet
            mine = [t for t in reversed(accepted) if address in (t.trxn.source, t.trxn.target)]
            return api.TransactionsGetResult(status=ok(), result=True, total_trxns_count=len(mine),
                                             transactions=mine[offset:offset + limit])

        def SmartContractCompile(self, source):
            if "class" not in source:
                return api.SmartContractCompileResult(status=general.APIResponse(code=1, message="not a Java class"))
            return api.SmartContractCompileResult(status=ok(), tokenStandard=0, byteCodeObjects=[
                general.ByteCodeObject(name="FakeContract", byteCode=BYTE_CODE)])

        def SmartContractGet(self, address):
            if address == TOKEN:  # wallet
                d = api.SmartContractDeploy(sourceCode="class FakeToken {}", hashState="", tokenStandard=4, lang=0,
                                            byteCodeObjects=[general.ByteCodeObject(name="FakeToken", byteCode=TOKEN_CODE)])
                return api.SmartContractGetResult(status=ok(), smartContract=api.SmartContract(address=address,
                                                                                               smartContractDeploy=d))
            if address not in deployed:
                return api.SmartContractGetResult(status=general.APIResponse(code=1, message="not found"))
            d = api.SmartContractDeploy(sourceCode="class FakeContract {}", hashState="", tokenStandard=0, lang=0,
                                        byteCodeObjects=[general.ByteCodeObject(name="FakeContract", byteCode=BYTE_CODE)])
            return api.SmartContractGetResult(status=ok(), smartContract=api.SmartContract(address=address,
                                                                                           smartContractDeploy=d))

        def ContractAllMethodsGet(self, byte_code_objects):
            if [b.byteCode for b in byte_code_objects or []] == [TOKEN_CODE]:  # wallet
                return api.ContractAllMethodsGetResult(code=0, message="", methods=[
                    general.MethodDescription(name=n, returnType=r, annotations=[],
                                              arguments=[general.MethodArgument(type=a, name=f"arg{i}", annotations=[])
                                                         for i, a in enumerate(types)])
                    for n, types, r in TOKEN_METHODS])
            if [b.byteCode for b in byte_code_objects or []] != [BYTE_CODE]:
                return api.ContractAllMethodsGetResult(code=1, message="unknown byte code")
            return api.ContractAllMethodsGetResult(code=0, message="", methods=[
                general.MethodDescription(name=n, returnType=r, annotations=[],
                                          arguments=[general.MethodArgument(type=a, name=f"arg{i}", annotations=[])
                                                     for i, a in enumerate(types)])
                for n, types, r in METHODS])

        def TransactionFlow(self, t):
            if t.smartContract is not None:
                return contract_flow(t)
            if t.type != api.TransactionType.TT_Transfer or t.currency != 1 or t.smartContract is not None:
                return refuse(f"not a CS transfer: type {t.type}, currency {t.currency}")
            if t.id != last_inner[0] + 1:
                return refuse(f"inner id {t.id}, expected {last_inner[0] + 1}")
            if t.balance is None or t.fee is None or abs(t.timeCreation - time.time() * 1000) > 600_000:
                return refuse("balance, fee or creation time missing or wrong")
            if not 0 <= t.amount.fraction < 10**18:
                return refuse(f"amount fraction {t.amount.fraction} out of range")
            # wallet: the user field, read as APIHandler::checkTransaction reads it, signed as fund.py does
            uf, note, delegated = t.userFields or b"", "", None
            if not uf:
                fields = b"\x00"
            elif uf[0] != 0:
                fields, note = struct.pack("<BI", 1, len(uf)) + uf, f", data {uf.decode('utf-8', 'replace')!r}"
            elif len(uf) == 15 and uf[:7] == struct.pack("<BBIB", 0, 1, 5, 1):
                delegated = struct.unpack("<Q", uf[7:])[0]
                fields, note = struct.pack("<BQ", 1, delegated), f", delegated {delegated}"
            else:
                return refuse(f"user fields not understood: {uf.hex()}")
            signed = b"".join([
                t.id.to_bytes(6, "little"), t.source, t.target,
                struct.pack("<iQ", t.amount.integral, t.amount.fraction),
                struct.pack("<H", t.fee.commission & 0xFFFF), struct.pack("<B", t.currency), fields,
            ])
            try:
                nacl.signing.VerifyKey(t.source).verify(signed, t.signature)
            except (nacl.exceptions.BadSignatureError, ValueError) as e:
                return refuse(f"bad signature: {e}")
            if delegated is not None:  # wallet: the node's delegation rules
                value = Decimal(t.amount.integral) + Decimal(t.amount.fraction) / 10**18
                if value < 5:
                    return refuse("The delegated amount is too low")
                if delegated == 2:
                    mine = [d for d in delegations if d[0] == t.source and d[1] == t.target and d[3] == 0]
                    if not mine or mine[0][2] < value:
                        return refuse("No delegated amount to withdraw")
                    mine[0][2] -= value
                    if mine[0][2] == 0:
                        delegations.remove(mine[0])
                    t.type = api.TransactionType.TT_RevokeDelegation
                elif delegated == 1 or delegated >= 4:
                    if 4 <= delegated < time.time():
                        return refuse("delegation expiry in the past")
                    until = 0 if delegated == 1 else delegated
                    same = [d for d in delegations if d[0] == t.source and d[1] == t.target and d[3] == until]
                    if same:
                        same[0][2] += value
                    else:
                        delegations.append([t.source, t.target, value, until])
                    t.type = api.TransactionType.TT_Delegation
                else:
                    return refuse(f"delegated value {delegated}")
            print(f"ACCEPTED: {base58.b58encode(t.source).decode()} -> {base58.b58encode(t.target).decode()} "
                  f"{t.amount.integral}.{t.amount.fraction:018d} CS, inner id {t.id}, max fee {t.fee.commission}{note}",
                  flush=True)
            last_inner[0] = t.id  # wallet: in a block
            t.poolNumber = head[0]
            t.fee = api.AmountCommission(commission=17279)
            accepted.append(api.SealedTransaction(id=api.TransactionId(poolSeq=head[0], index=0), trxn=t))
            return api.TransactionFlowResult(status=ok(), roundNum=1, id=api.TransactionId(poolSeq=head[0], index=0),
                                             fee=general.Amount(integral=0, fraction=8_740_000_000_000_000))

    def amount_of(value):
        return general.Amount(integral=int(value), fraction=int((value - int(value)) * 10**18))

    def wallet_handlers(handler):
        # wallet: delegations and the token cache
        def WalletDataGet(address):
            out = [api.DelegatedItem(wallet=d[1], sum=amount_of(d[2]), validUntil=d[3], fromTime=0, coeff=0)
                   for d in delegations if d[0] == address]
            into = [api.DelegatedItem(wallet=d[0], sum=amount_of(d[2]), validUntil=d[3], fromTime=0, coeff=0)
                    for d in delegations if d[1] == address]
            delegated = api.Delegated(incoming=amount_of(sum((d[2] for d in delegations if d[1] == address), Decimal(0))),
                                      outgoing=amount_of(sum((d[2] for d in delegations if d[0] == address), Decimal(0))),
                                      donors=into, recipients=out)
            return api.WalletDataGetResult(status=ok(), walletData=api.WalletData(
                walletId=1, balance=general.Amount(integral=100, fraction=0), lastTransactionId=last_inner[0],
                delegated=delegated))

        def TokenBalancesGet(address):
            if args.no_token_cache:
                return no_cache(api.TokenBalancesResult)
            b = token_balances.get(base58.b58encode(address).decode(), Decimal(0))
            return api.TokenBalancesResult(status=ok(), balances=[] if b == 0 else [
                api.TokenBalance(token=TOKEN, code="FTK", name="Fake Token", balance=str(b))])

        def TokenInfoGet(token):
            if args.no_token_cache:
                return no_cache(api.TokenInfoResult)
            if token != TOKEN:
                return api.TokenInfoResult(status=general.APIResponse(code=1, message=""))
            return api.TokenInfoResult(status=ok(), token=api.TokenInfo(
                address=TOKEN, code="FTK", name="Fake Token", totalSupply="1000.00", owner=base58.b58decode(TEST_HOLDER),
                transfersCount=len(token_transfers), transactionsCount=len(token_transfers), holdersCount=len(token_balances),
                tokenStandard=4))

        def TokenWalletTransfersGet(token, address, offset, limit):
            if args.no_token_cache:
                return no_cache(api.TokenTransfersResult)
            if token != TOKEN:
                return api.TokenTransfersResult(status=general.APIResponse(code=1, message=""))
            mine = [x for x in reversed(token_transfers) if address in (x.sender, x.receiver)]
            return api.TokenTransfersResult(status=ok(), count=len(mine), transfers=mine[offset:offset + limit])

        for f in (WalletDataGet, TokenBalancesGet, TokenInfoGet, TokenWalletTransfersGet):
            setattr(handler, f.__name__, f)
        return handler

    server = make_server(api.API, wallet_handlers(Handler()), host="127.0.0.1", port=args.port, proto_factory=TBinaryProtocolFactory(),
                         trans_factory=TBufferedTransportFactory(), client_timeout=None)
    print(f"fake node listening on 127.0.0.1:{args.port}", flush=True)
    server.serve()


if __name__ == "__main__":
    main()
