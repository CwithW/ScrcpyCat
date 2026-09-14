#!/system/bin/sh
SKIPUNZIP=0
set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/action.sh" 0 0 0755
set_perm "$MODPATH/system/bin/scrcpycatctl" 0 0 0755
for file in "$MODPATH"/bin/*/scrcpycat-agent; do set_perm "$file" 0 0 0755; done
ui_print "ScrcpyCat Agent installed. Reboot to enroll the configured device."
