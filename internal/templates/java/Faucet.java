import com.credits.scapi.annotations.Getter;
import com.credits.scapi.v3.SmartContract;

import java.math.BigDecimal;
import java.util.HashSet;

/**
 * A faucet that decides from its own balance: claim() pays GIFT CS to a new address while the
 * contract holds at least MIN_BALANCE CS, and refuses once it runs dry. Anyone refills it with a
 * plain CS transfer (payable).
 *
 * getBalance() reads the balance at the moment the method runs, so the result of claim() is not a
 * function of the earlier calls alone: it also depends on transfers to and from the contract and on
 * where the call falls among them. The confidants that execute a call live agree on that moment.
 * A node that rebuilds the state later by executing the calls again (while it syncs) reads the
 * balance as of the block it is replaying, which already includes that block's payouts: claim()
 * can fail there where it succeeded live, and that node's state no longer matches the one the chain
 * declares. The community node therefore fetches such states from the confidants instead of trusting
 * its own re-execution (docs/differences.md, "Contract states while syncing").
 *
 * Prefer bookkeeping in the state, as TipJar does, when the contract can; use getBalance() when
 * the real balance is what matters, and keep that dependency in mind.
 */
public class Faucet extends SmartContract {

    private static final BigDecimal GIFT = new BigDecimal("5");
    // the gift plus a reserve (the fee of the payout is charged to the caller, not to the contract)
    private static final BigDecimal MIN_BALANCE = new BigDecimal("10");

    private final HashSet<String> served = new HashSet<>();
    private String lastBalanceSeen = "";

    public Faucet() {
        super();
    }

    @Override
    public String payable(BigDecimal amount, byte[] userData) {
        return "refilled with " + amount.toPlainString() + " CS";
    }

    /** Pays GIFT CS to the caller, once per address; returns how many addresses were served. */
    public int claim() {
        BigDecimal balance = getBalance(contractAddress);
        // kept in the state: what this execution saw, so the dependency shows in the state itself
        lastBalanceSeen = balance.toPlainString();
        if (served.contains(initiator)) {
            throw new RuntimeException(initiator + " was already served");
        }
        if (balance.compareTo(MIN_BALANCE) < 0) {
            throw new RuntimeException("the faucet is dry: " + balance.toPlainString() + " CS, needs " + MIN_BALANCE);
        }
        sendTransaction(contractAddress, initiator, GIFT.doubleValue());
        served.add(initiator);
        return served.size();
    }

    @Getter
    public int getServed() {
        return served.size();
    }

    @Getter
    public boolean wasServed(String address) {
        return served.contains(address);
    }

    /** The balance now: as a getter it reads the current balance, which a later claim() may not see. */
    @Getter
    public BigDecimal balance() {
        return getBalance(contractAddress);
    }

    @Getter
    public String getLastBalanceSeen() {
        return lastBalanceSeen;
    }
}
