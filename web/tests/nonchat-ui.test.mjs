import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';
import React from 'react';
import * as jsx from 'react/jsx-runtime';
import { renderToStaticMarkup } from 'react-dom/server';
import * as intl from 'next-intl';
import * as icons from 'lucide-react';

export function nonChatComponents() {
 const compile=(file,deps={})=>{const m={exports:{}};vm.runInNewContext(ts.transpileModule(fs.readFileSync(new URL(file,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,jsx:ts.JsxEmit.ReactJSX}}).outputText,{exports:m.exports,module:m,require:name=>{if(!(name in deps))throw Error(`Unexpected dependency ${name}`);return deps[name];}});return m.exports;};
 const deps={'react/jsx-runtime':jsx,'next-intl':intl,'lucide-react':icons,'@/api/nonchat':compile('../src/api/nonchat.ts')};
 return {...compile('../src/components/modules/log/NonChatUsage.tsx',deps),...compile('../src/components/common/NonChatStats.tsx',deps),...compile('../src/components/modules/log/TransportDetails.tsx',deps)};
}
export function renderNonChat(element,locale='zh_hans') { const messages=JSON.parse(fs.readFileSync(new URL(`../public/locale/${locale}.json`,import.meta.url),'utf8'));return renderToStaticMarkup(React.createElement(intl.NextIntlClientProvider,{locale:locale==='zh_hans'?'zh-CN':locale==='zh_hant'?'zh-TW':'en',messages,timeZone:'UTC'},element)); }
const {NonChatUsage,NonChatStats,TransportDetails}=nonChatComponents();
const partial={basis:'processing',completeness:'complete',input_tokens:451,total_tokens:451,counts:{embedding_input_items:8,embedding_vectors:8,embedding_dimensions:4096},first_byte_ms:0,response_ms:1000};
for(const locale of ['en','zh_hans','zh_hant']) test(`non-chat UI uses real ${locale} labels, keeps unknown separate from zero`,()=>{
 const html=renderNonChat(React.createElement(NonChatUsage,{summary:partial,requestMS:2000,details:true}),locale);
 assert.ok(html.includes('4096')||html.includes('4,096'));
 assert.ok(html.includes('0ms'));assert.ok(html.includes('1.00s'));assert.ok(html.includes('4.00'));assert.ok(html.includes('225.50'));
 const messages=JSON.parse(fs.readFileSync(new URL(`../public/locale/${locale}.json`,import.meta.url),'utf8')).nonChat;
 assert.ok(html.includes(messages.notReported));assert.ok(!html.includes('MISSING_MESSAGE'));assert.ok(!html.includes('TPS'));
 const zero=renderNonChat(React.createElement(NonChatUsage,{summary:{...partial,output_tokens:0},requestMS:2000,details:true}),locale);
 assert.ok(zero.includes(`${messages.output}: 0`));
});
test('total-only and billed counters remain visible without invented direction',()=>{
 const html=renderNonChat(React.createElement(NonChatUsage,{summary:{basis:'billed',completeness:'complete',total_tokens:1000,counts:{rerank_documents:30,rerank_results:0}},evidence:{processing:{},billed:{total_tokens:1000},units:{search_units:1},response_state:'complete'},requestMS:2000,details:true}),'en');
 assert.match(html,/1,000/);assert.match(html,/15.00/);assert.match(html,/Search units/);assert.match(html,/not reported/i);
});
test('aggregates show observed zero and coverage, and omit unknown history',()=>{
 assert.equal(renderNonChat(React.createElement(NonChatStats,{metrics:{}})), '');
 const html=renderNonChat(React.createElement(NonChatStats,{metrics:{rerank_results:0,rerank_results_observed:2,unattributed_token:300,usage_reported_requests:2}}),'en');
 assert.match(html,/>0<\/strong>/);assert.match(html,/300/);assert.match(html,/2/);
});

for (const locale of ['en','zh_hans','zh_hant']) test(`transport details use real ${locale} labels and observed phases`,()=>{
 const origin=Date.UTC(2026,10,16);
 const network={timing_source:'task_submission',preparation_at:origin,dispatch_at:origin+10,response_headers_at:origin+40,first_body_byte_at:origin+1050,first_effective_output_at:origin+1150,terminal_at:origin+1250,connection_reused:true,request_id:'safe-request-id'};
 const task={transport:'catie_tasks',submission_state:'accepted',status:'completed',done:true,cursor:2,poll_count:3,query_retry_count:0,cancel_state:'not_requested',deadline_at:origin+1800000};
 const html=renderNonChat(React.createElement(TransportDetails,{diagnostics:{selected_attempt:2,network,task,first_write_at:origin+1160,attempts:[{attempt:2,protocol:'chat',model:'full-prefix/safe-fixture',endpoint_id:'fixture-endpoint',key_id:7,network,task}]}}),locale);
 const labels=JSON.parse(fs.readFileSync(new URL(`../public/locale/${locale}.json`,import.meta.url),'utf8')).log.card.forensics.transport;
 for (const label of [labels.title,labels.submissionNetwork,labels.statuses.completed,labels.selected,labels.firstWrite,labels.phases.first_body_byte_at]) assert.ok(html.includes(label),label);
 assert.match(html,/1\.050s/);
 assert.match(html,/<summary[^>]*>.*full-prefix\/safe-fixture/);
 assert.doesNotMatch(html,/MISSING_MESSAGE|forensics\.transport|undefined|NaN/);
 assert.ok(!html.includes(labels.phases.dns_start_at));
 assert.ok(!html.includes(labels.phases.tls_start_at));
 assert.ok(!html.includes('Token/s'));
});

test('transport details keep legacy unknown and task failure separate from completion',()=>{
 assert.equal(renderNonChat(React.createElement(TransportDetails,{diagnostics:{version:3,attempts:[{attempt:1,protocol:'chat'}]}}),'en'),'');
 const html=renderNonChat(React.createElement(TransportDetails,{diagnostics:{task:{transport:'catie_tasks',submission_state:'unknown',done:false,cancel_state:'unconfirmed'}}}),'en');
 assert.match(html,/Unknown|Not confirmed/);
 assert.doesNotMatch(html,/Completed|First body byte|First gateway write accepted/);
});
