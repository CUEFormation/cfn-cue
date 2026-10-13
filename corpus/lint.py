"""Pinned cfn-lint driver with source configuration disabled.

Examples can include cfn-lint metadata that loads custom rules or suppresses
errors. Treat that metadata as template data, never as harness configuration.
"""
import sys

from cfnlint.config import ConfigMixIn
from cfnlint.runner import Runner


class CorpusConfig(ConfigMixIn):
    def set_template_args(self, template):
        self._template_args = {}

    @property
    def template_args(self):
        return {}

    @template_args.setter
    def template_args(self, template):
        self._template_args = {}


if __name__ == "__main__":
    Runner(CorpusConfig(sys.argv[1:])).cli()
