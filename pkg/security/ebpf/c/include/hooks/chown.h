#ifndef _HOOKS_CHOWN_H_
#define _HOOKS_CHOWN_H_

#include "constants/syscall_macro.h"
#include "helpers/discarders.h"
#include "helpers/filesystem.h"
#include "helpers/syscalls.h"

int __attribute__((always_inline)) trace__sys_chown(const char *filename, uid_t user, gid_t group) {
    if (is_discarded_by_pid()) {
        return 0;
    }

    struct policy_t policy = fetch_policy(EVENT_CHOWN);
    struct syscall_cache_t syscall = {
        .type = EVENT_CHOWN,
        .policy = policy,
        .setattr = {
            .user = user,
            .group = group }
    };

    collect_syscall_ctx(&syscall, SYSCALL_CTX_ARG_STR(0) | SYSCALL_CTX_ARG_INT(1) | SYSCALL_CTX_ARG_INT(2), (void *)filename, (void *)&user, (void *)&group);
    cache_syscall(&syscall);

    return 0;
}

HOOK_SYSCALL_ENTRY3(lchown, const char *, filename, uid_t, user, gid_t, group) {
    return trace__sys_chown(filename, user, group);
}

HOOK_SYSCALL_ENTRY3(fchown, int, fd, uid_t, user, gid_t, group) {
    return trace__sys_chown(NULL, user, group);
}

HOOK_SYSCALL_ENTRY3(chown, const char *, filename, uid_t, user, gid_t, group) {
    return trace__sys_chown(filename, user, group);
}

HOOK_SYSCALL_ENTRY3(lchown16, const char *, filename, uid_t, user, gid_t, group) {
    return trace__sys_chown(filename, user, group);
}

HOOK_SYSCALL_ENTRY3(fchown16, int, fd, uid_t, user, gid_t, group) {
    return trace__sys_chown(NULL, user, group);
}

HOOK_SYSCALL_ENTRY3(chown16, const char *, filename, uid_t, user, gid_t, group) {
    return trace__sys_chown(filename, user, group);
}

HOOK_SYSCALL_ENTRY4(fchownat, int, dirfd, const char *, filename, uid_t, user, gid_t, group) {
    return trace__sys_chown(filename, user, group);
}

int __attribute__((always_inline)) sys_chown_ret(void *ctx, int retval) {
    struct syscall_cache_t *syscall = pop_syscall(EVENT_CHOWN);
    if (!syscall) {
        return 0;
    }

    if (IS_UNHANDLED_ERROR(retval)) {
        return 0;
    }

    set_file_layer(syscall->resolver.dentry, &syscall->setattr.file);

    struct chown_event_t event = {
        .syscall.retval = retval,
        .syscall_ctx.id = syscall->ctx_id,
        .file = syscall->setattr.file,
        .uid = syscall->setattr.user,
        .gid = syscall->setattr.group,
    };

    struct proc_cache_t *entry = fill_process_context(&event.process);
    fill_container_context(entry, &event.container);
    fill_span_context(&event.span);

    // dentry resolution in setattr.h

    send_event(ctx, EVENT_CHOWN, event);

    return 0;
}

HOOK_SYSCALL_EXIT(lchown) {
    int retval = SYSCALL_PARMRET(ctx);
    return sys_chown_ret(ctx, retval);
}

HOOK_SYSCALL_EXIT(fchown) {
    int retval = SYSCALL_PARMRET(ctx);
    return sys_chown_ret(ctx, retval);
}

HOOK_SYSCALL_EXIT(chown) {
    int retval = SYSCALL_PARMRET(ctx);
    return sys_chown_ret(ctx, retval);
}

HOOK_SYSCALL_EXIT(lchown16) {
    int retval = SYSCALL_PARMRET(ctx);
    return sys_chown_ret(ctx, retval);
}

HOOK_SYSCALL_EXIT(fchown16) {
    int retval = SYSCALL_PARMRET(ctx);
    return sys_chown_ret(ctx, retval);
}

HOOK_SYSCALL_EXIT(chown16) {
    int retval = SYSCALL_PARMRET(ctx);
    return sys_chown_ret(ctx, retval);
}

HOOK_SYSCALL_EXIT(fchownat) {
    int retval = SYSCALL_PARMRET(ctx);
    return sys_chown_ret(ctx, retval);
}

SEC("tracepoint/handle_sys_chown_exit")
int tracepoint_handle_sys_chown_exit(struct tracepoint_raw_syscalls_sys_exit_t *args) {
    return sys_chown_ret(args, args->ret);
}

#endif
