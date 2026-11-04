// node-key-check DIR: reads DIR/NodePrivate.txt and DIR/NodePublic.txt the way the community node
// does at its start (client/config/config.cpp, Config::readKeys: the same steps, with the node's own
// cscrypto, base58 and libsodium), then signs and verifies a message with the key, as the node signs.
// Prints "ok <public key>" when the node would start with these files without asking anything, and
// otherwise what the node would do; exit 0 or 1. The wallet's tests and the privnet run use it to
// check the node key files the wallet writes (spec 4.4).
//
// Built against a node build tree, as seed-vectors.cpp:
//   N=node B=build-node
//   g++ -std=c++17 -o node-key-check node-key-check.cpp -I$N/cscrypto/include \
//       -I$N/cscrypto/third-party/base58/include -I$B/libsodium/Build/Release/x64/include \
//       $B/cscrypto/libcscrypto.a $B/cscrypto/third-party/base58/libbase58.a \
//       $B/cscrypto/third-party/blake2/libblake2.a $B/libsodium/Build/Release/x64/lib/libsodium.a -lpthread
#include <cscrypto/cscrypto.hpp>
#include <base58.h>
#include <cstdio>
#include <fstream>
#include <string>
#include <vector>

int main(int argc, char** argv) {
    if (argc != 2) {
        std::fprintf(stderr, "usage: node-key-check DIR\n");
        return 2;
    }
    if (!cscrypto::cryptoInit()) {
        return 2;
    }
    const std::string dir = argv[1];
    // First read private (Config::readKeys)
    std::ifstream skFile(dir + "/NodePrivate.txt");
    if (!skFile.is_open()) {
        std::puts("no NodePrivate.txt: the node would ask to generate a key (\"g\" or \"q\")");
        return 1;
    }
    std::string sk58;
    std::vector<uint8_t> sk;
    std::getline(skFile, sk58);
    skFile.close();
    DecodeBase58(sk58, sk);
    if (sk.size() < cscrypto::kPrivateKeySize) {
        std::puts("Bad private key file: the node would stop");
        return 1;
    }
    if (sk.size() > cscrypto::kPrivateKeySize) {
        std::puts("The key file seems to be encrypted: the node would ask a password");
        return 1;
    }
    auto privateKey = cscrypto::PrivateKey::readFromBytes(sk);
    if (!privateKey) {
        std::puts("Bad private key: the node would stop");
        return 1;
    }
    auto publicKey = cscrypto::getMatchingPublic(privateKey);
    const std::string pk58 = EncodeBase58(publicKey.data(), publicKey.data() + publicKey.size());
    // then the public key file: its first line must be pk58
    std::ifstream pkFile(dir + "/NodePublic.txt");
    bool pkGood = false;
    if (pkFile.is_open()) {
        std::string pkFileCont;
        std::getline(pkFile, pkFileCont);
        pkFile.close();
        pkGood = (pkFileCont == pk58);
    }
    if (!pkGood) {
        std::puts("The PUBLIC key file not found or doesn't contain a valid key: the node would stop and ask \"f\"");
        return 1;
    }
    // the key works: the seed half and the public half agree (the node signs with the first and
    // announces the second)
    const std::string msg = "node-key-check";
    auto sig = cscrypto::generateSignature(privateKey, reinterpret_cast<const cscrypto::Byte*>(msg.data()), msg.size());
    if (!cscrypto::verifySignature(sig, publicKey, reinterpret_cast<const cscrypto::Byte*>(msg.data()), msg.size())) {
        std::puts("the signature does not verify with the public key: the halves of the key disagree");
        return 1;
    }
    std::printf("ok %s\n", pk58.c_str());
    return 0;
}
