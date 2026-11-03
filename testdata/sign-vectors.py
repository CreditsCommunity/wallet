#!/usr/bin/env python3
"""Signing vectors for the wallet's transactions with a user field, built the way the community
node's ci/testnet/fund.py and samples/python/token_client.py build them (CreditsCommunity/node,
AGPL-3.0), independently of the wallet's Go code:

  - delegations (fund.py transfer(..., delegated=...)): the delegated user field (id 5) signs as
    count 1 + u64, and travels in Transaction.userFields as flag 0, count 1, id u32, type 1, u64;
  - user data (a string user field, id 1): signs as count 1 + u32 size + bytes (fund.py: "a string
    user field signs as u32 size + bytes"); the API carries the text as it is (APIHandler reads a
    userFields whose first byte is not 0 as the text);
  - a token transfer (token_client.py call): one user field, the Thrift binary serialization of the
    SmartContractInvocation, as u32 size + bytes.

  python3 testdata/sign-vectors.py > testdata/sign-vectors.txt

internal/tx/vectors_test.go checks that the wallet signs the same bytes. ed25519 signatures are
deterministic, so the signatures are compared as well.
"""
import os
import struct

import base58
import nacl.signing
import thriftpy2
from thriftpy2.protocol import TBinaryProtocolFactory
from thriftpy2.utils import serialize

HERE = os.path.dirname(os.path.abspath(__file__))
IDL = os.path.join(HERE, "..", "third-party", "thrift-interface-definitions")
api = thriftpy2.load(os.path.join(IDL, "api.thrift"), module_name="api_thrift", include_dirs=[IDL])
general = api.general

CURRENCY_CS = 1
UF_DELEGATED = 5
UF_TYPE_INTEGER = 1
MAX_FEE = 0x4C66  # encode_max_fee(1.0)
TARGET = base58.b58decode("u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w")
# a made-up contract address (any 32 bytes)
TOKEN = base58.b58decode("9vX2wH7ytrbbo1KtNa13dUz3nvtJqaCH4oGrr2NEsBqd")


def key():
    with open(os.path.join(HERE, "test-key.txt")) as f:
        secret = base58.b58decode(f.read().strip())
    return nacl.signing.SigningKey(secret[:32])


def head(sk, inner_id, target, amount_int, amount_frac):
    return b"".join([
        inner_id.to_bytes(6, "little"), bytes(sk.verify_key), target,
        struct.pack("<iQ", amount_int, amount_frac), struct.pack("<H", MAX_FEE), struct.pack("<B", CURRENCY_CS),
    ])


def emit(name, signed, wire, sk):
    print(f"{name}.signed: {signed.hex()}")
    print(f"{name}.wire: {wire.hex()}")
    print(f"{name}.signature: {sk.sign(signed).signature.hex()}")


def main():
    sk = key()
    print(f"# python3 testdata/sign-vectors.py; key testdata/test-key.txt, source {base58.b58encode(bytes(sk.verify_key)).decode()}")
    # fund.py transfer(src, dst, amount, delegated=value)
    for name, inner, amount, value in (("delegate", 8, 1000, 1), ("delegate-until", 9, 500, 1893456000),
                                       ("withdraw", 10, 1000, 2)):
        signed = head(sk, inner, TARGET, amount, 0) + struct.pack("<BQ", 1, value)
        wire = struct.pack("<BBIBQ", 0, 1, UF_DELEGATED, UF_TYPE_INTEGER, value)
        emit(name, signed, wire, sk)
    # a string user field: u32 size + bytes
    text = "invoice 42 — grüße\n\tpaid".encode("utf-8")
    signed = head(sk, 11, TARGET, 12, 500_000_000_000_000_000) + struct.pack("<BI", 1, len(text)) + text
    emit("data", signed, text, sk)
    # token_client.py call: transfer(String to, BigDecimal amount)
    sci = api.SmartContractInvocation(method="transfer", params=[
        general.Variant(v_string="u6PJ8DtQuPFnfmwHbGFULQ4u4EgjDiyYKjVEsynXq2w"), general.Variant(v_big_decimal="250.5")],
        usedContracts=[], forgetNewState=False, smartContractDeploy=None, version=1)
    code = serialize(sci, TBinaryProtocolFactory())
    signed = head(sk, 12, TOKEN, 0, 0) + struct.pack("<BI", 1, len(code)) + code
    emit("token-transfer", signed, code, sk)


if __name__ == "__main__":
    main()
