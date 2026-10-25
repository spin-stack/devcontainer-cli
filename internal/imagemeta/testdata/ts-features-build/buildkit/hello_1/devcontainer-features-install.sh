#!/bin/sh
set -e

on_exit () {
	[ $? -eq 0 ] && exit
	echo 'ERROR: Feature "./hello" (Unknown) failed to install!'
}

trap on_exit EXIT

echo ===========================================================================
echo '(!) WARNING: Using the deprecated Feature "hello". This Feature will no longer receive any further updates/support.
(!) WARNING: This feature has been renamed. Please update the reference in devcontainer.json to "hello2".'
echo 'Feature       : ./hello'
echo 'Description   : '
echo 'Id            : Unknown'
echo 'Version       : '
echo 'Documentation : '
echo 'Options       :'
echo '    GREETING="hey"
    FLAG="false"'
echo ===========================================================================

set -a
. ../devcontainer-features.builtin.env
. ./devcontainer-features.env
set +a

chmod +x ./install.sh
./install.sh
