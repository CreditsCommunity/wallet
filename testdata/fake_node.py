#!/usr/bin/env python3
"""A stand-in for a node's API that checks the transfers the sample clients send, offline.

Copied from the community node, CreditsCommunity/node samples/clients/testdata/fake_node.py
(AGPL-3.0), with a few calls added for the wallet (marked "wallet:"): SyncStateGet, PoolListGet,
PoolInfoGet, ActualFeeGet, WalletBalanceGet, TransactionsGet, and WalletTransactionsCountGet
following the accepted transfers. --idl defaults to this repository's submodule.

  fake_node.py [--port 19090] [--idl DIR]

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
VARIANT_OF = {"int": "v_int", "java.lang.String": "v_string"}


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

    def contract_flow(t):
        sci = t.smartContract
        if t.type == api.TransactionType.TT_ContractCall and sci.forgetNewState:
            # a getter: nothing is signed or sent
            if t.target not in deployed:
                return refuse("getter on an unknown contract")
            return run_method(sci, getter=True)
        if t.type not in (api.TransactionType.TT_ContractDeploy, api.TransactionType.TT_ContractCall) \
                or t.currency != 1 or t.id != LAST_INNER_ID + 1 or t.fee is None or t.balance is None:
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

    last_inner = [LAST_INNER_ID]  # wallet: the last inner id moves on with the accepted transfers
    head = [1000]
    accepted = []

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
            mine = [t for t in reversed(accepted) if address in (t.trxn.source, t.trxn.target)]
            return api.TransactionsGetResult(status=ok(), result=True, total_trxns_count=len(mine),
                                             transactions=mine[offset:offset + limit])

        def SmartContractCompile(self, source):
            if "class" not in source:
                return api.SmartContractCompileResult(status=general.APIResponse(code=1, message="not a Java class"))
            return api.SmartContractCompileResult(status=ok(), tokenStandard=0, byteCodeObjects=[
                general.ByteCodeObject(name="FakeContract", byteCode=BYTE_CODE)])

        def SmartContractGet(self, address):
            if address not in deployed:
                return api.SmartContractGetResult(status=general.APIResponse(code=1, message="not found"))
            d = api.SmartContractDeploy(sourceCode="class FakeContract {}", hashState="", tokenStandard=0, lang=0,
                                        byteCodeObjects=[general.ByteCodeObject(name="FakeContract", byteCode=BYTE_CODE)])
            return api.SmartContractGetResult(status=ok(), smartContract=api.SmartContract(address=address,
                                                                                           smartContractDeploy=d))

        def ContractAllMethodsGet(self, byte_code_objects):
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
            if t.userFields:
                return refuse("unexpected user fields")
            if t.balance is None or t.fee is None or abs(t.timeCreation - time.time() * 1000) > 600_000:
                return refuse("balance, fee or creation time missing or wrong")
            if not 0 <= t.amount.fraction < 10**18:
                return refuse(f"amount fraction {t.amount.fraction} out of range")
            signed = b"".join([
                t.id.to_bytes(6, "little"), t.source, t.target,
                struct.pack("<iQ", t.amount.integral, t.amount.fraction),
                struct.pack("<H", t.fee.commission & 0xFFFF), struct.pack("<B", t.currency), b"\x00",
            ])
            try:
                nacl.signing.VerifyKey(t.source).verify(signed, t.signature)
            except (nacl.exceptions.BadSignatureError, ValueError) as e:
                return refuse(f"bad signature: {e}")
            print(f"ACCEPTED: {base58.b58encode(t.source).decode()} -> {base58.b58encode(t.target).decode()} "
                  f"{t.amount.integral}.{t.amount.fraction:018d} CS, inner id {t.id}, max fee {t.fee.commission}",
                  flush=True)
            last_inner[0] = t.id  # wallet: in a block
            t.poolNumber = head[0]
            t.fee = api.AmountCommission(commission=17279)
            accepted.append(api.SealedTransaction(id=api.TransactionId(poolSeq=head[0], index=0), trxn=t))
            return api.TransactionFlowResult(status=ok(), roundNum=1, id=api.TransactionId(poolSeq=head[0], index=0),
                                             fee=general.Amount(integral=0, fraction=8_740_000_000_000_000))

    server = make_server(api.API, Handler(), host="127.0.0.1", port=args.port, proto_factory=TBinaryProtocolFactory(),
                         trans_factory=TBufferedTransportFactory(), client_timeout=None)
    print(f"fake node listening on 127.0.0.1:{args.port}", flush=True)
    server.serve()


if __name__ == "__main__":
    main()
