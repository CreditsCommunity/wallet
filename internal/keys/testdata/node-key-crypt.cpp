// node-key-crypt: the node's own encryption of NodePrivate.txt (cs::keyfile::encrypt and decrypt of
// client/config/keyfilecrypt.cpp, the code behind the node's --encryptkey and its "[1] Encrypt with
// password" choice; client/config/config.cpp, Config::readKeys), for the wallet's tests:
//
//   node-key-crypt encrypt DIR PASSWORD   DIR/NodePrivate.txt (plain) is encrypted in place, as the
//                                         node does with --encryptkey (base58, std::ofstream, no
//                                         newline): the salted format, 104 bytes
//   node-key-crypt encrypt-legacy DIR PASSWORD   the same with cscrypto's PrivateKey::getEncrypted,
//                                         as older and official nodes do: 88 bytes, the fixed salt
//   node-key-crypt open DIR PASSWORD      DIR/NodePrivate.txt (encrypted, either format) is opened as
//                                         the node opens it at its start; prints "ok <public key
//                                         base58>" or exit 1
//
// The password is passed as the node passes it: its bytes up to the first NUL (std::strlen).
// Built against a node build tree (a community node with client/config/keyfilecrypt.cpp):
//   N=node B=build-node
//   g++ -std=c++17 -o node-key-crypt node-key-crypt.cpp $N/client/config/keyfilecrypt.cpp \
//       -I$N/client/config -I$N/cscrypto/include \
//       -I$N/cscrypto/third-party/base58/include -I$B/libsodium/Build/Release/x64/include \
//       $B/cscrypto/libcscrypto.a $B/cscrypto/third-party/base58/libbase58.a \
//       $B/cscrypto/third-party/blake2/libblake2.a $B/libsodium/Build/Release/x64/lib/libsodium.a -lpthread
#include <cscrypto/cscrypto.hpp>
#include <keyfilecrypt.hpp>
#include <base58.h>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <string>
#include <vector>

int main(int argc, char** argv) {
    if (argc != 4) {
        std::fprintf(stderr, "usage: node-key-crypt encrypt|encrypt-legacy|open DIR PASSWORD\n");
        return 2;
    }
    if (!cscrypto::cryptoInit()) {
        return 2;
    }
    const std::string cmd = argv[1], path = std::string(argv[2]) + "/NodePrivate.txt";
    const char* pass = argv[3];
    std::ifstream skFile(path);
    if (!skFile.is_open()) {
        std::puts("no NodePrivate.txt");
        return 1;
    }
    std::string sk58;
    std::vector<uint8_t> sk;
    std::getline(skFile, sk58);
    skFile.close();
    DecodeBase58(sk58, sk);
    if (cmd == "encrypt" || cmd == "encrypt-legacy") {
        if (sk.size() != cscrypto::kPrivateKeySize) {
            std::puts("not a plain key");
            return 1;
        }
        auto privateKey = cscrypto::PrivateKey::readFromBytes(sk);
        auto skBytes = cmd == "encrypt" ? cs::keyfile::encrypt(privateKey, pass, std::strlen(pass))
                                        : privateKey.getEncrypted(pass, std::strlen(pass));
        if (skBytes.empty()) {
            std::puts("not encrypted");
            return 1;
        }
        std::string out = EncodeBase58(skBytes.data(), skBytes.data() + skBytes.size());
        std::ofstream file(path);  // writeFile of config.cpp
        file << out;
        file.close();
        std::printf("encrypted %zu bytes\n", skBytes.size());
        return 0;
    }
    if (cmd == "open") {
        if (sk.size() <= cscrypto::kPrivateKeySize) {
            std::puts("not an encrypted key");
            return 1;
        }
        auto privateKey = cs::keyfile::decrypt(sk, pass, std::strlen(pass));
        if (!privateKey) {
            std::puts("Incorrect password (or corrupted file)");
            return 1;
        }
        auto publicKey = cscrypto::getMatchingPublic(privateKey);
        const std::string msg = "node-key-crypt";
        auto sig = cscrypto::generateSignature(privateKey, reinterpret_cast<const cscrypto::Byte*>(msg.data()), msg.size());
        if (!cscrypto::verifySignature(sig, publicKey, reinterpret_cast<const cscrypto::Byte*>(msg.data()), msg.size())) {
            std::puts("the signature does not verify");
            return 1;
        }
        std::printf("ok %s\n", EncodeBase58(publicKey.data(), publicKey.data() + publicKey.size()).c_str());
        return 0;
    }
    std::fprintf(stderr, "unknown command %s\n", cmd.c_str());
    return 2;
}
