package org.mythicove.isles;

import net.milkbowl.vault.economy.Economy;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;

final class VaultHook {
    record Balance(String provider, double balance, String formatted, String currency) {}
    Balance balance(Player player) {
        var registered = Bukkit.getServicesManager().getRegistration(Economy.class);
        if (registered == null || !registered.getProvider().isEnabled()) return null;
        var economy = registered.getProvider();
        if (!economy.hasAccount(player)) return null;
        double balance = economy.getBalance(player);
        if (!Double.isFinite(balance)) return null;
        return new Balance(economy.getName(), balance, economy.format(balance), economy.currencyNamePlural());
    }
}
