import com.credits.scapi.annotations.Getter;
import com.credits.scapi.v3.SmartContract;

import java.math.BigDecimal;
import java.util.HashMap;

import static java.math.BigDecimal.ZERO;

/**
 * A payable contract: anyone sends CS to it with a plain transfer, it records who paid how much,
 * and its owner withdraws the CS with sendTransaction().
 *
 * - A CS transfer whose target is a contract makes the node call payable(amount, userData) with
 *   {@code initiator} set to the payer. Returning normally accepts the payment: the CS stay on the
 *   contract's balance. Throwing refuses it: the node gives the CS back to the payer, who still pays
 *   the fee (WalletsCache, a contract state that was not updated).
 * - sendTransaction(contractAddress, to, amount) makes the contract pay from its own balance. The
 *   emitted transfer is part of the call's result: it lands only if the call succeeds. Its fee is
 *   charged to the caller (here the owner), not to the contract.
 * - The contract decides what it can withdraw from its own bookkeeping (what was paid in minus what
 *   was taken out), not from getBalance(): its result depends only on the calls before it, so every
 *   node that executes it, now or later while syncing, computes the same. See Faucet.java for the
 *   other way and what it costs.
 */
public class TipJar extends SmartContract {

    private final String owner;
    private final HashMap<String, BigDecimal> paid = new HashMap<>();
    private BigDecimal received = ZERO;
    private BigDecimal withdrawn = ZERO;
    private int payments;

    public TipJar() {
        super();
        owner = initiator;
    }

    @Override
    public String payable(BigDecimal amount, byte[] userData) {
        if (amount.signum() <= 0) {
            throw new RuntimeException("nothing paid");
        }
        paid.put(initiator, paidBy(initiator).add(amount));
        received = received.add(amount);
        payments += 1;
        return "thank you, " + initiator;
    }

    /** The owner takes amount CS out; returns what is left to withdraw. */
    public BigDecimal withdraw(BigDecimal amount) {
        if (!initiator.equals(owner)) {
            throw new RuntimeException("only the owner " + owner + " can withdraw");
        }
        BigDecimal available = available();
        if (amount.signum() <= 0 || amount.compareTo(available) > 0) {
            throw new RuntimeException("can withdraw up to " + available.toPlainString() + ", not " + amount.toPlainString());
        }
        sendTransaction(contractAddress, owner, amount.doubleValue());
        withdrawn = withdrawn.add(amount);
        return available();
    }

    @Getter
    public BigDecimal paidBy(String address) {
        BigDecimal amount = paid.get(address);
        return amount == null ? ZERO : amount;
    }

    @Getter
    public BigDecimal getReceived() {
        return received;
    }

    @Getter
    public BigDecimal available() {
        return received.subtract(withdrawn);
    }

    @Getter
    public int getPayments() {
        return payments;
    }

    @Getter
    public String getOwner() {
        return owner;
    }
}
