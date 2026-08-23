# DEPRECATED: This Python prototype has been superseded by the Go daemon
# at cmd/mb8611-poller/. See .plans/Designs/Mb8611Poller/README.md for
# the design and Plans/Mb8611Poller/ for the implementation history.
# Retained for protocol reference only; do not deploy.

import requests
import hashlib
import hmac
import time
import math
import pprint
import contextlib
from collections.abc import Mapping
from typing import Final, Literal

# --- Configuration ---
HOST = '192.168.100.1'
USERNAME = "automation"
PASSWORD = "changeme"  # <-- Replace this

SOAP_LOGIN = "http://purenetworks.com/HNAP1/Login"
SOAP_GET_STATUS = "http://purenetworks.com/HNAP1/GetMultipleHNAPs"

# Disable HTTPS warnings
requests.packages.urllib3.disable_warnings()

SHARED_HEADERS: Final[Mapping[str, str]] = {
    'accept': 'application/json',
    'cache-control': 'no-cache',
    'connection': 'keep-alive',
    'content-type': 'application/json',
    'x-requested-with': 'XMLHttpRequest'
}

MultipleHNAPAction = Literal['GetHomeAddress', 'GetHomeConnection', 'GetMotoLagStatus',
                             'GetMotoStatusConnectionInfo', 'GetMotoStatusDownstreamChannelInfo',
                             'GetMotoStatusLog', 'GetMotoStatusLogXXX', 'GetMotoStatusSecAccount',
                             'GetMotoStatusSecXXX', 'GetMotoStatusSoftware',
                             'GetMotoStatusStartupSequence', 'GetMotoStatusUpstreamChannelInfo']

MUST_BE_CALLED_FROM_MULTIPLE: Final[set[MultipleHNAPAction]] = {
    'GetHomeAddress', 'GetHomeConnection', 'GetMotoLagStatus', 'GetMotoStatusConnectionInfo',
    'GetMotoStatusDownstreamChannelInfo', 'GetMotoStatusLog', 'GetMotoStatusLogXXX',
    'GetMotoStatusSecAccount', 'GetMotoStatusSecXXX', 'GetMotoStatusSoftware',
    'GetMotoStatusStartupSequence', 'GetMotoStatusUpstreamChannelInfo'
}


def make_soap_action_uri(action: str) -> str:
    """Return the SOAP action URI for the given action."""
    return f'"http://purenetworks.com/HNAP1/{action}"'


def make_hnap_auth(action: str, private_key: str) -> str:
    """Create the value required for the ``HNAP_AUTH`` header."""
    current_time = str(math.floor(time.time_ns() / 1000000) % 2000000000000)
    auth = hmac.new(private_key.encode(), (current_time + make_soap_action_uri(action)).encode(),
                    'md5')
    return f'{auth.hexdigest().upper()} {current_time}'


class CallHNAPError(Exception):
    def __init__(self, response: requests.Response) -> None:
        super().__init__(f'HNAP error: {response}')


class LockedError(Exception):
    pass


class LoginFailed(RuntimeError):
    pass


class HnapClient(object):
    def __init__(self, password: str, host: str, username: str) -> None:
        self.hnap_endpoint = f'https://{host}/HNAP/'
        self.host = host
        self.username = username
        self.password = password
        self.session = requests.Session()
        self.session.headers.update(SHARED_HEADERS)
        self.privateKey = 'withoutloginkey'

    def hnap_call(self, action, payload, check=True):
        with contextlib.suppress(KeyError):
            self.session.cookies.clear(HOST, '/HNAP1', 'Secure')

        if action in MUST_BE_CALLED_FROM_MULTIPLE:
            return self.hnap_multi_call(action, check=False)

        headers = {
            'HNAP_AUTH': make_hnap_auth(action, self.privateKey),
            'SOAPACTION': make_soap_action_uri(action)
        }

        r = self.session.post(self.hnap_endpoint, headers=headers, json=payload, verify=False)
        r.raise_for_status()

        res = r.json()
        if check and res[f'{action}Response'][f'{action}Result'] != 'OK':
            raise CallHNAPError(res)

        return res

    def hnap_multi_call(self, actions, check=True):
        return self.hnap_call(
            'GetMultipleHNAPs',
            {'GetMultipleHNAPs': dict.fromkeys(actions, '')},
            check=check)

    def login(self) -> None:
        response = self.hnap_call('Login', {
            'Login': {
                'Action': 'request',
                'Username': self.username,
                'LoginPassword': '',
                'Captcha': '',
                'PrivateLogin': 'LoginPassword',
            }
        }, check=False)

        if response['LoginResponse']['LoginResult'] == 'FAILED':
            raise LockedError

        public_key = response['LoginResponse']['PublicKey']
        challenge = response['LoginResponse']['Challenge']

        if 'Cookie' in response['LoginResponse']:
            self.session.cookies.set('uid', response['LoginResponse']['Cookie'], path='/', domain=self.host)

        encodedKey = str(public_key + self.password).encode()
        self.privateKey = hmac.new(encodedKey, challenge.encode(), 'md5').hexdigest().upper()
        self.session.cookies.set('PrivateKey', self.privateKey, path='/', domain=self.host)

        for path in ('/font', '/js/SOAP', '/js', '/css', '/', '/image', '/HNAP1'):
            self.session.cookies.set('','Secure', path=path, domain=self.host, rest={'HttpOnly': True})

        response = self.hnap_call('Login', {
            'Login': {
                'Action': 'request',
                'Username': self.username,
                'LoginPassword': hmac.new(self.privateKey.encode(), challenge.encode(), 'md5').hexdigest().upper(),
                'Captcha': '',
                'PrivateLogin': 'LoginPassword',
            }
        }, check=False)

        if response['LoginResponse']['LoginResult'] != 'OK':
            raise LoginFailed


if __name__ == "__main__":
    client = HnapClient(PASSWORD, HOST, USERNAME)
    client.login()

    res = client.hnap_call('GetMotoStatusConnectionInfo', {'GetMotoStatusConnectionInfo': {}}, check=False)
