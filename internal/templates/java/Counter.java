import com.credits.scapi.annotations.Getter;
import com.credits.scapi.v3.SmartContract;

import java.math.BigDecimal;

/**
 * The simplest useful contract: a counter with a note, owned by whoever deployed it.
 *
 * - The fields are the contract's state. The executor serializes the whole object after every
 *   call that changes it, and the block records the hash of the new state.
 * - The constructor runs once, at deployment: {@code initiator} is then the deployer.
 * - A public method is a call. Sent as a transaction ({@code call}) it is executed by the
 *   confidants, its new state is kept, and the sender pays the fee. Run as a getter ({@code get})
 *   it runs on one node against the current state, which is then thrown away: nothing is paid,
 *   nothing changes. {@code @Getter} only documents which methods are meant to be read that way.
 * - Arguments arrive with the types the method declares: {@code add(int)} wants a Variant v_int.
 *
 * SmartContract v3 (the current API) makes every contract declare payable(): what happens when
 * someone sends CS to the contract with a plain transfer.
 */
public class Counter extends SmartContract {

    private final String owner;
    private int value;
    private String note = "";

    public Counter() {
        super();
        owner = initiator;
    }

    /** Anyone may count: adds one and returns the new value. */
    public int increment() {
        value += 1;
        return value;
    }

    /** Adds n (at least 1) and returns the new value. */
    public int add(int n) {
        if (n < 1) {
            throw new RuntimeException("n must be at least 1, got " + n);
        }
        value += n;
        return value;
    }

    /** Only the owner may write the note. A call that throws changes nothing (the fee is still paid). */
    public void setNote(String text) {
        if (!initiator.equals(owner)) {
            throw new RuntimeException("only the owner " + owner + " can set the note");
        }
        note = text;
    }

    @Getter
    public int getValue() {
        return value;
    }

    @Getter
    public String getNote() {
        return note;
    }

    @Getter
    public String getOwner() {
        return owner;
    }

    /** This contract takes no CS: payable() throws, and the node gives the CS of a transfer back to its sender (minus the fee). */
    @Override
    public String payable(BigDecimal amount, byte[] userData) {
        throw new RuntimeException("Counter does not accept CS");
    }
}
