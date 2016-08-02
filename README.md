# Cloud Foundry Review Application Service

## Usage

1. Create a [GitHub access token](https://help.github.com/articles/creating-an-access-token-for-command-line-use) with permission to read and write to your repo

1. Create an instance of the `review-app` service

    ```sh
    $ cf create-service review-app review-app my-review-app \
        -c '{"owner": "github-user", "repo": "github-repo", "token": "github-token"}'
    ```
