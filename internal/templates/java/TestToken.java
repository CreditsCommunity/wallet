import com.credits.scapi.annotations.Getter;
import com.credits.scapi.v1.ExtensionTokenStandard;
import com.credits.scapi.v3.SmartContract;

import java.math.BigDecimal;
import java.util.HashMap;
import java.util.Map;

import static java.math.BigDecimal.ZERO;

/**
 * Community Test Token (CTT): a token FOR TESTS ONLY, on a private network or a test network.
 * It is not a real asset; the samples never deploy it on mainnet. For a token of your own start
 * from Token.java instead.
 *
 * It implements ExtensionTokenStandard v1 on SmartContract v3, the standard the node and the
 * executor recognise as a token (token standard 4): with [api] tokens_cache=true the node lists it
 * in TokensListGet, and TokenInfoGet / TokenBalancesGet report its holders and balances.
 *
 * - The whole supply, 1'000'000 CTT with 2 decimals, goes to the deployer.
 * - transfer, approve (sets the allowance), transferFrom (spends it), balanceOf, allowance.
 * - Amounts are BigDecimal (Variant v_big_decimal) with at most 2 decimals; addresses are base58
 *   strings (v_string).
 * - It cannot be bought: payable() refuses CS.
 */
public class TestToken extends SmartContract implements ExtensionTokenStandard {

    private static final String NAME = "Community Test Token";
    private static final String SYMBOL = "CTT";
    private static final int DECIMALS = 2;
    private static final long SUPPLY = 1_000_000L;

    private final String owner;
    private final HashMap<String, BigDecimal> balances = new HashMap<>();
    private final HashMap<String, Map<String, BigDecimal>> allowed = new HashMap<>();
    private BigDecimal totalCoins;
    private boolean frozen;

    public TestToken() {
        super();
        owner = initiator;
        totalCoins = scaled(new BigDecimal(SUPPLY));
        balances.put(owner, totalCoins);
    }

    @Getter
    @Override
    public String getName() {
        return NAME;
    }

    @Getter
    @Override
    public String getSymbol() {
        return SYMBOL;
    }

    @Getter
    @Override
    public int getDecimal() {
        return DECIMALS;
    }

    @Getter
    @Override
    public BigDecimal totalSupply() {
        return totalCoins;
    }

    @Getter
    @Override
    public BigDecimal balanceOf(String owner) {
        BigDecimal balance = balances.get(owner);
        return balance == null ? scaled(ZERO) : balance;
    }

    @Getter
    @Override
    public BigDecimal allowance(String owner, String spender) {
        Map<String, BigDecimal> spenders = allowed.get(owner);
        BigDecimal amount = spenders == null ? null : spenders.get(spender);
        return amount == null ? scaled(ZERO) : amount;
    }

    @Override
    public boolean setFrozen(boolean isFrozen) {
        if (!initiator.equals(owner)) {
            throw new RuntimeException("only the owner " + owner + " can freeze the token");
        }
        frozen = isFrozen;
        return true;
    }

    @Override
    public boolean transfer(String to, BigDecimal amount) {
        move(initiator, to, checked(amount));
        return true;
    }

    /** initiator moves amount from {@code from} to {@code to}, within what {@code from} approved for it. */
    @Override
    public boolean transferFrom(String from, String to, BigDecimal amount) {
        BigDecimal value = checked(amount);
        BigDecimal allowance = allowance(from, initiator);
        if (allowance.compareTo(value) < 0) {
            throw new RuntimeException("allowance is " + allowance.toPlainString() + ", less than " + value.toPlainString());
        }
        move(from, to, value);
        allowed.get(from).put(initiator, allowance.subtract(value));
        return true;
    }

    /** Sets (does not add to) what spender may move from the initiator's balance. */
    @Override
    public void approve(String spender, BigDecimal amount) {
        BigDecimal value = amount.signum() == 0 ? scaled(ZERO) : checked(amount);
        allowed.computeIfAbsent(initiator, k -> new HashMap<>()).put(spender, value);
    }

    @Override
    public boolean burn(BigDecimal amount) {
        BigDecimal value = checked(amount);
        move(initiator, null, value);
        totalCoins = totalCoins.subtract(value);
        return true;
    }

    @Override
    public void register() {
        balances.putIfAbsent(initiator, scaled(ZERO));
    }

    @Override
    public String payable(BigDecimal amount, byte[] userData) {
        throw new RuntimeException("CTT is a test token: it cannot be bought");
    }

    // to == null burns
    private void move(String from, String to, BigDecimal value) {
        if (frozen) {
            throw new RuntimeException("the token is frozen");
        }
        BigDecimal source = balanceOf(from);
        if (source.compareTo(value) < 0) {
            throw new RuntimeException("balance of " + from + " is " + source.toPlainString() + ", less than " + value.toPlainString());
        }
        if (to != null && to.equals(from)) {
            return;
        }
        balances.put(from, source.subtract(value));
        if (to != null) {
            balances.put(to, balanceOf(to).add(value));
        }
    }

    // a positive amount with at most DECIMALS decimals, at the token's scale
    private static BigDecimal checked(BigDecimal amount) {
        if (amount == null || amount.signum() <= 0) {
            throw new RuntimeException("the amount must be positive");
        }
        if (amount.stripTrailingZeros().scale() > DECIMALS) {
            throw new RuntimeException("the amount has more than " + DECIMALS + " decimals");
        }
        return scaled(amount);
    }

    private static BigDecimal scaled(BigDecimal value) {
        return value.setScale(DECIMALS, BigDecimal.ROUND_UNNECESSARY);
    }
}
