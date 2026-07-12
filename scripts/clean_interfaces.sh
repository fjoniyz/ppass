#!/bin/bash

# Ensure the script is run with root privileges
if [[ $EUID -ne 0 ]]; then
   echo "This script must be run as root (use sudo)." 
   exit 1
fi

# Find all interfaces starting with 'v-'
# We exclude the '@' part often seen in 'ip link' output (e.g., v-eth0@if12)
INTERFACES=$(ip -o link show | awk -F': ' '{print $2}' | awk -F'@' '{print $1}' | grep '^v-')

if [ -z "$INTERFACES" ]; then
    echo "No interfaces starting with 'v-' were found."
    exit 0
fi

echo "The following interfaces will be deleted:"
echo "$INTERFACES"
echo "---------------------------------------"

for IFACE in $INTERFACES; do
    echo "Deleting interface: $IFACE"
    ip link delete "$IFACE" 2>/dev/null
    
    if [ $? -eq 0 ]; then
        echo "Successfully removed $IFACE."
    else
        echo "Failed to remove $IFACE (it might have been removed by a parent process)."
    fi
done

echo "Cleanup complete."
