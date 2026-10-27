#!/bin/bash
set -e

echo "Building docker image"

docker build --tag vorfahrt:latest .

docker tag vorfahrt:latest git.home.saibotd.com/saibotd/vorfahrt:latest

echo "Pushing docker image"
docker push "git.home.saibotd.com/saibotd/vorfahrt:latest"
