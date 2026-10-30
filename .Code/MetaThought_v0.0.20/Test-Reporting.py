"""One offline stdlib fixture for saved-report extraction and rendering.

Usage: python Test-Reporting.py NEW_EVIDENCE_FOLDER
Creates only synthetic labelled databases and new report files in that folder.
No MetaThought executable, Go suite, provider, credentials, or saved run is used.
"""
import contextlib
import importlib.util
import io
import json
import socket
import sqlite3
import sys
import unittest
from decimal import Decimal
from pathlib import Path
from unittest.mock import patch

sys.dont_write_bytecode = True
SOURCE = Path(__file__).resolve().parent
MODEL = 'synthetic-report-model'
RATES = {'models': {MODEL: {'input': '1', 'cached_input': '0.25', 'output': '2'}},
         'note': 'Synthetic arithmetic fixture; not live provider pricing.'}


def load_script(name):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), SOURCE / name)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def usage(cached=True):
    detail = {'cached_tokens': 40} if cached else {}
    raw = {'prompt_tokens': 100, 'completion_tokens': 10, 'total_tokens': 110,
           'prompt_tokens_details': detail, 'completion_tokens_details': {'reasoning_tokens': 3}}
    saved = {'input_tokens': 100, 'output_tokens': 10, 'total_tokens': 110,
             'input_tokens_details': detail, 'output_tokens_details': {'reasoning_tokens': 3}}
    return raw, saved


class SyntheticLog:
    def __init__(self, path, schema):
        self.db = sqlite3.connect(path)
        for statement in schema:
            self.db.execute(statement)
        self.sequence = 0

    def event(self, event, details, table='hive_log', scope='queen'):
        self.sequence += 1
        self.db.execute(f'INSERT INTO {table}(ts,scope,event,model,outcome,details) VALUES(?,?,?,?,?,?)',
                        (f'2026-10-27T00:00:{self.sequence:02d}Z', scope, event, MODEL,
                         details.get('outcome'), json.dumps(details)))

    def call(self, label, table='hive_log', scope='queen', cached=True,
             no_usage=False, refusal=False, http_error=False, unfinished=False):
        self.event('llm.start', {'model': MODEL, 'request': json.dumps({'model': MODEL,
                   'messages': [{'role': 'user', 'content': 'SYNTHETIC ' + label}]})}, table, scope)
        if unfinished:
            return
        raw, saved = usage(cached)
        response = {'model': MODEL, 'choices': [{'message': {'content': 'SYNTHETIC reply'}}]}
        if not no_usage:
            response['usage'] = raw
        body = '<html>SYNTHETIC unavailable</html>' if http_error else json.dumps(response)
        self.event('llm.response', {'model': MODEL, 'status_code': 503 if http_error else 200, 'body': body}, table, scope)
        finish = {'model': MODEL, 'outcome': 'answered', 'reply': 'SYNTHETIC reply'}
        if http_error:
            finish.update(outcome='error', kind='http_error', error='SYNTHETIC 503')
        else:
            if not no_usage:
                finish['usage'] = saved
            if refusal:
                finish.update(outcome='refusal', refusal='SYNTHETIC refusal with measured usage')
        self.event('llm.finish', finish, table, scope)

    def close(self):
        self.db.commit()
        self.db.close()


def create_state(path, schema, campaign):
    with sqlite3.connect(path) as db:
        for statement in schema:
            db.execute(statement)
        db.execute('INSERT INTO mq_inquiries(id,kind,owner_id,contract,status,created) VALUES(1,?,?,?,?,?)',
                   ('evolution' if campaign else 'experiment', 1, 'SYNTHETIC confirmed meta contract', 'returned', 'fixture-time'))
        for drone, score in ((7, 0.75), (8, 0.4)):
            db.execute('INSERT INTO mq_assessments(inquiry_id,subject_key,drone_id,criterion,score,evidence,uncertainty,created) VALUES(1,?,?,?,?,?,?,?)',
                       ('trial:1', drone, 'intellectual_influence', score,
                        f'SYNTHETIC influence evidence from drone {drone}', f'SYNTHETIC uncertainty {drone}', 'fixture-time'))
        if campaign:
            db.execute('INSERT INTO mq_evolutions(id,problem_type,status,state,created,updated) VALUES(1,?,?,?,?,?)',
                       ('synthetic', 'completed', json.dumps({'id': 1, 'status': 'completed', 'phase': 'completed'}), 'fixture-time', 'fixture-time'))
            db.execute('INSERT INTO mq_methods(id,evolution_id,operator,target,chromosome,config,created) VALUES(1,1,?,?,?,?,?)',
                       ('reference', '', 0, json.dumps({'prompts': {}, 'chromosomes': []}), 'fixture-time'))
            db.execute('INSERT INTO mq_trials(evolution_id,method_id,status,evidence,assessment,created,updated) VALUES(1,1,?,?,?,?,?)',
                       ('completed', '{}', '', 'fixture-time', 'fixture-time'))


class ReportingFixture(unittest.TestCase):
    evidence_folder = None

    def test_saved_reports_preserve_scores_rejections_roles_and_unknown_costs(self):
        schema = json.loads((SOURCE / 'config.json').read_text(encoding='utf-8-sig'))['schema']
        root = self.evidence_folder
        rates_path = root / 'synthetic-rates.json'
        rates_path.write_text(json.dumps(RATES), encoding='utf-8')
        base, campaign = root / 'base', root / 'campaign'
        for directory in (base, campaign):
            directory.mkdir()
            create_state(directory / 'state.db', schema['state'], directory == campaign)

        log = SyntheticLog(base / 'debug.db', schema['logs'])
        log.call('invalid score', scope='drone')
        log.event('assessment_parse', {'outcome': 'error', 'error': 'SYNTHETIC bad score'}, scope='drone')
        log.call('invalid contract and missing cache count', cached=False)
        log.event('contract_rejected', {'outcome': 'format_error', 'error': 'SYNTHETIC bad contract'})
        log.call('invalid return and missing usage', no_usage=True)
        log.event('review_rejected', {'outcome': 'invalid_selection', 'error': 'SYNTHETIC invalid candidate'})
        log.call('plain HTML provider error', http_error=True)
        log.call('interrupted latest call', unfinished=True)
        log.close()

        log = SyntheticLog(campaign / 'debug.db', schema['logs'])
        scorer = {'actor_kind': 'drone', 'drone_id': 7, 'inquiry_id': 1, 'subject_key': 'trial:1', 'prompt_key': 'assess'}
        log.event('meta_call_start', scorer, 'mq_log', 'meta_queen')
        log.call('meta drone score', 'mq_log', 'meta_queen')
        log.event('meta_call_done', scorer | {'outcome': 'answered'}, 'mq_log', 'meta_queen')
        log.event('assessment_parse', scorer | {'outcome': 'parsed'}, 'mq_log', 'meta_queen')
        commit = {'actor_kind': 'drone', 'drone_id': 8, 'inquiry_id': 1, 'name': 'mq_commit', 'class': 'choice'}
        log.event('decision.start', commit, 'mq_log', 'meta_queen')
        log.event('decision.question.start', commit, 'mq_log', 'meta_queen')
        log.call('meta drone commitment', 'mq_log', 'meta_queen')
        log.event('decision.question.finish', commit | {'outcome': 'format_error', 'error': 'SYNTHETIC bad choice'}, 'mq_log', 'meta_queen')
        log.event('decision.finish', {'outcome': 'error', 'kind': 'format_error', 'error': 'SYNTHETIC bad choice'}, 'mq_log', 'meta_queen')
        log.event('meta_call_start', {'prompt_key': 'mq_return'}, 'mq_log', 'meta_queen')
        log.call('meta Queen return', 'mq_log', 'meta_queen')
        log.event('meta_call_done', {'prompt_key': 'mq_return', 'outcome': 'answered'}, 'mq_log', 'meta_queen')
        log.event('decision.start', {'questions': 1}, 'mq_log', 'meta_queen')
        log.event('decision.finish', {'outcome': 'error', 'kind': 'invalid_request', 'error': 'SYNTHETIC pre-call validation'}, 'mq_log', 'meta_queen')
        log.event('meta_call_start', scorer, 'mq_log', 'meta_queen')
        log.event('meta_call_done', scorer | {'outcome': 'error', 'error': 'SYNTHETIC pre-call key configuration'}, 'mq_log', 'meta_queen')
        log.event('decision.start', commit, 'mq_log', 'meta_queen')
        log.event('decision.question.start', commit, 'mq_log', 'meta_queen')
        log.event('decision.question.finish', commit | {'outcome': 'configuration_error', 'error': 'SYNTHETIC pre-call question key'}, 'mq_log', 'meta_queen')
        log.event('decision.finish', {'outcome': 'error', 'kind': 'configuration_error', 'error': 'SYNTHETIC pre-call question key'}, 'mq_log', 'meta_queen')
        log.call('provider refusal with usage', 'mq_log', 'meta_queen', refusal=True)
        log.close()

        databases = list(root.glob('*/*.db'))
        snapshots = {path: path.read_bytes() for path in databases}
        with patch.object(socket, 'socket', side_effect=AssertionError('network forbidden')), \
                patch.object(socket, 'create_connection', side_effect=AssertionError('network forbidden')):
            hive_reader = load_script('Analyze-SavedExperiment.py')
            evolution_reader = load_script('Analyze-Evolution.py')
            for module, directory, prefix in ((hive_reader, base, root / 'base-report'),
                                              (evolution_reader, campaign, root / 'campaign-report')):
                output = io.StringIO()
                with patch.object(sys, 'argv', [module.__file__, str(directory), str(rates_path), str(prefix)]), contextlib.redirect_stdout(output):
                    module.main()
                (root / (directory.name + '-stdout.json')).write_text(output.getvalue(), encoding='utf-8')

        base_report = json.loads((root / 'base-report.json').read_text(encoding='utf-8'))
        campaign_report = json.loads((root / 'campaign-report.json').read_text(encoding='utf-8'))
        self.assertEqual(len(base_report['calls']), 5)
        self.assertEqual(len(base_report['unpriced_calls']), 4)
        self.assertEqual(Decimal(base_report['estimated_total_usd']), Decimal('0.00009'))
        self.assertEqual(base_report['rejections']['event_counts'],
                         {'assessment_parse': 1, 'contract_rejected': 1, 'review_rejected': 1, 'provider': 2})
        self.assertIsNone(base_report['calls'][1]['token_counters']['cached'])
        self.assertEqual(base_report['calls'][1]['unpriced_reason'], 'missing_input_output_or_cached_usage')
        self.assertIsNone(base_report['calls'][2]['token_counters']['input'])
        self.assertEqual(base_report['calls'][3]['response_raw'], '<html>SYNTHETIC unavailable</html>')
        self.assertTrue(base_report['calls'][3]['response_parse_error'])
        self.assertEqual(base_report['calls'][4]['unpriced_reason'], 'unfinished_call')
        unknown_totals = evolution_reader.totals(base_report['calls'][1:])['all']
        self.assertIsNone(unknown_totals['known_estimated_usd'])

        calls = campaign_report['calls']
        self.assertEqual(len(calls), 4, 'meta_call wrappers must not become extra calls')
        self.assertEqual([call['role'] for call in calls],
                         ['meta_drone_assessment', 'meta_drone_commitment', 'meta_queen', 'meta_queen'])
        self.assertEqual([calls[0]['drone_id'], calls[1]['drone_id']], [7, 8])
        self.assertEqual(calls[0]['subject_key'], 'trial:1')
        self.assertEqual(campaign_report['rejections']['event_counts'], {'decisions': 5, 'provider': 1})
        self.assertEqual(campaign_report['failure_counts']['decision_parser_error'], 1)
        self.assertEqual(campaign_report['failure_counts']['provider_refusal'], 1)
        self.assertEqual(Decimal(campaign_report['totals']['campaign']['all']['known_estimated_usd']), Decimal('0.00036'))
        self.assertEqual(Decimal(calls[-1]['estimated_usd']), Decimal('0.00009'), 'refusal with usage is not free')
        self.assertEqual(campaign_report['totals']['by_role']['meta_queen']['calls'], 2)
        no_call_errors = [event for event in campaign_report['failure_events']
                          if 'SYNTHETIC pre-call' in event['details'].get('error', '')]
        self.assertEqual(len(no_call_errors), 4)
        self.assertTrue(all(event['associated_call_id'] is None for event in no_call_errors),
                        'validation/key errors without llm.start must not attach to the prior successful call')
        self.assertTrue(all(call['native_cost_usd'] is None for call in base_report['calls'] + calls))
        self.assertEqual(evolution_reader.rejection_category('decision.question.finish',
                         {'outcome': 'transport_error', 'error': 'synthetic'}), 'decisions')
        for name in ('base', 'campaign'):
            markdown = (root / (name + '-report.md')).read_text(encoding='utf-8')
            self.assertIn('Inquiry 1 · trial:1 · drone 7 · intellectual_influence', markdown)
            self.assertIn('actual score: 0.75', markdown)
            self.assertIn('actual score: 0.4', markdown)
            self.assertIn('SYNTHETIC influence evidence from drone 7', markdown)
            self.assertIn('SYNTHETIC uncertainty 8', markdown)
            self.assertIn('not unique rejected-call totals', markdown)
            self.assertIn('Native billed dollars are unavailable', markdown)
            self.assertNotIn('(No assessment saved.)', markdown)
        for path, before in snapshots.items():
            self.assertEqual(path.read_bytes(), before, f'analyzer wrote synthetic source database {path}')
        observation = {'fixture': 'synthetic offline report extraction', 'base_calls': len(base_report['calls']),
                       'base_unpriced_calls': len(base_report['unpriced_calls']), 'base_rejection_events': base_report['rejections']['event_counts'],
                       'campaign_calls': len(calls), 'campaign_roles': [call['role'] for call in calls],
                       'campaign_rejection_events': campaign_report['rejections']['event_counts'],
                       'campaign_estimated_usd': campaign_report['totals']['campaign']['all']['known_estimated_usd'],
                       'databases_unchanged': len(databases), 'network_calls': 0, 'native_cost': 'unavailable'}
        (root / 'observation.json').write_text(json.dumps(observation, indent=2), encoding='utf-8')
        print(json.dumps(observation, separators=(',', ':')))


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit('Usage: python Test-Reporting.py NEW_EVIDENCE_FOLDER')
    ReportingFixture.evidence_folder = Path(sys.argv[1]).resolve()
    ReportingFixture.evidence_folder.mkdir(exist_ok=False)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(ReportingFixture)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    raise SystemExit(0 if result.wasSuccessful() else 1)
