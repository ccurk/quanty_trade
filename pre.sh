#!/bin/bash

git fetch --all

git reset --hard origin/main

git pull origin main 

chmod +x server_deploy_*

chmod +x pre.sh