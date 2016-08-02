#!/bin/sh

set -e

mv $HOME/bin/cli $HOME/bin/cf
cf-review-app
