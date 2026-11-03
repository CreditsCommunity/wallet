// Seed phrase test vectors from the node's own code (cscrypto mnemonic.cpp, keysderivation.cpp):
// fixed, made-up master seeds -> 24 words and the derived key pair. Its output is
// seed-vectors.json; the keys are public test values, never use them for funds.
//
// Built against a node build tree (cscrypto b91762f, the node's submodule), for example:
//   N=node/cscrypto B=build-node
//   g++ -std=c++17 -o seed-vectors seed-vectors.cpp -I$N/include -I$B/libsodium/Build/Release/x64/include \
//       -I$N/third-party/blake2/sse $B/cscrypto/libcscrypto.a $B/cscrypto/third-party/blake2/libblake2.a \
//       $B/libsodium/Build/Release/x64/lib/libsodium.a -lpthread
//   (echo '['; ./seed-vectors; echo ']') > seed-vectors.json
#include <cscrypto/cscrypto.hpp>
#include <cscrypto/mnemonic.hpp>
#include <cscrypto/keysderivation.hpp>
#include <cstdio>
#include <cstring>
using namespace cscrypto;
static void hex(const unsigned char* p, size_t n) { for (size_t i = 0; i < n; ++i) printf("%02x", p[i]); }
int main() {
    if (sodium_init() < 0) return 1;
    for (int v = 0; v < 6; ++v) {
        unsigned char s[32];
        for (int i = 0; i < 32; ++i) {
            switch (v) {
            case 0: s[i] = 0; break;
            case 1: s[i] = i; break;
            case 2: s[i] = 0xff; break;
            case 3: s[i] = (unsigned char)(i * 37 + 11); break;
            case 4: s[i] = (unsigned char)(255 - i * 3); break;
            default: s[i] = (unsigned char)((i * i * 13 + 7) ^ 0x5a); break;
            }
        }
        keys_derivation::MasterSeed seed;
        std::memcpy(seed.data(), s, 32);
        auto words = mnemonic::masterSeedToWords(seed);
        // back from the words, as the node does
        auto back = mnemonic::wordsToMasterSeed(words);
        keys_derivation::accessMasterSeed(back);
        if (std::memcmp(back.data(), s, 32) != 0) { fprintf(stderr, "round trip failed\n"); return 1; }
        keys_derivation::lockMasterSeed(back);
        keys_derivation::accessMasterSeed(seed);
        auto kp = keys_derivation::deriveKeyPair(seed, 0);
        auto sk = kp.second.access();
        printf("{\"master_seed\": \""); hex(s, 32);
        printf("\", \"words\": \"");
        for (size_t i = 0; i < words.size(); ++i) printf("%s%s", i ? " " : "", words[i]);
        printf("\", \"public\": \""); hex(kp.first.data(), 32);
        printf("\", \"secret\": \""); hex(sk.data(), 64);
        printf("\"}%s\n", v < 5 ? "," : "");
    }
    return 0;
}
