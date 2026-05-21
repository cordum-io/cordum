from enum import Enum


class ShadowAgentFindingCiProvider(str, Enum):
    BUILDKITE = "buildkite"
    CIRCLECI = "circleci"
    GITHUB_ACTIONS = "github_actions"
    GITLAB_CI = "gitlab_ci"
    JENKINS = "jenkins"
    OTHER = "other"

    def __str__(self) -> str:
        return str(self.value)
