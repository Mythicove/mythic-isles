package org.mythicove.isles;

import net.luckperms.api.LuckPerms;
import net.luckperms.api.LuckPermsProvider;
import net.luckperms.api.node.types.InheritanceNode;
import net.luckperms.api.node.NodeType;
import java.util.UUID;
import java.util.List;
import java.util.concurrent.CompletableFuture;

final class LuckPermsHook {
    private final LuckPerms api = LuckPermsProvider.get();
    List<String> groups() { return api.getGroupManager().getLoadedGroups().stream().map(group -> group.getName()).sorted().limit(500).toList(); }
    List<String> playerGroups(UUID uuid) { var user = api.getUserManager().getUser(uuid); return user == null ? List.of() : user.getNodes(NodeType.INHERITANCE).stream().filter(node -> node.getContexts().isEmpty()).map(InheritanceNode::getGroupName).sorted().toList(); }
    String primaryGroup(UUID uuid) {
        var user = api.getUserManager().getUser(uuid);
        return user == null ? "" : user.getPrimaryGroup();
    }
    CompletableFuture<Void> setGroup(UUID uuid, String group, boolean grant) {
        return api.getGroupManager().loadGroup(group).thenCompose(found -> {
            if (found.isEmpty()) return CompletableFuture.failedFuture(new IllegalArgumentException("Group does not exist"));
            return api.getUserManager().modifyUser(uuid, user -> {
                var node = InheritanceNode.builder(group).build();
                if (grant) user.data().add(node); else user.data().remove(node);
            });
        });
    }
}
