import { useEffect, useState, useRef } from 'react';
import { Send, Bot, User, Loader2, Sparkles } from 'lucide-react';
import { apiFetch, makeWsUrl, type BusPacket } from '../lib/api';
import { Card } from '../components/ui/card';
import clsx from 'clsx';

interface Message {
    id: string;
    role: 'user' | 'assistant';
    text: string;
    status: 'sending' | 'pending' | 'complete' | 'error';
    timestamp: number;
}

const Chat = () => {
    const [messages, setMessages] = useState<Message[]>([]);
    const [input, setInput] = useState('');
    const scrollRef = useRef<HTMLDivElement>(null);
    const wsRef = useRef<WebSocket | null>(null);

    // Auto-scroll to bottom
    useEffect(() => {
        if (scrollRef.current) {
            scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
        }
    }, [messages]);

    useEffect(() => {
        let retry = 0;
        let closed = false;
        const connect = () => {
            if (closed) return;
            const ws = new WebSocket(makeWsUrl());
            wsRef.current = ws;
            
            ws.onerror = () => {
                console.warn("WebSocket error, check API key / gateway availability");
            };
            ws.onclose = () => {
                if (closed) return;
                const delay = Math.min(5000, 500 * Math.pow(2, retry++));
                setTimeout(connect, delay);
            };
            ws.onopen = () => {
                retry = 0;
            };
    
            ws.onmessage = async (event) => {
                try {
                    const packet: BusPacket = JSON.parse(event.data);
                    const result = packet.jobResult || packet.payload?.job_result;
    
                    if (result) {
                        setMessages(prev => prev.map(msg => {
                            const resultJobId = result.jobId || result.job_id;
                            if (msg.id === resultJobId) {
                                fetchResult(resultJobId);
                                return { ...msg, status: 'complete' }; 
                            }
                            return msg;
                        }));
                    }
                } catch (e) {
                    console.error("WS Error", e);
                }
            };
        };
        connect();
        return () => {
            closed = true;
            wsRef.current?.close();
        };
    }, []);

    const fetchResult = async (jobId: string) => {
        try {
            const res = await apiFetch(`/api/v1/jobs/${jobId}`);
            if (res.ok) {
                const data = await res.json();
                if (data.result && data.result.response) {
                     setMessages(prev => prev.map(msg => {
                        if (msg.id === jobId) {
                            return { 
                                ...msg, 
                                role: 'assistant', // Transform the placeholder user msg? No, we need a new bubble.
                                // Actually, simpler logic:
                                // 1. User sends -> adds User bubble.
                                // 2. We add a dummy "Thinking..." Assistant bubble with the job ID.
                                // 3. When result comes, we update that Assistant bubble.
                                text: data.result.response,
                                status: 'complete'
                            };
                        }
                        return msg;
                    }));
                }
            }
        } catch (e) {
            console.error(e);
        }
    };

    const sendMessage = async () => {
        if (!input.trim()) return;

        const text = input;
        setInput('');

        // 1. Add User Message
        const tempId = Date.now().toString();
        setMessages(prev => [...prev, {
            id: tempId,
            role: 'user',
            text: text,
            status: 'complete',
            timestamp: Date.now()
        }]);

        try {
            // 2. Submit Job
            const res = await apiFetch(`/api/v1/jobs`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    prompt: text,
                    topic: 'job.chat.simple' // Using simple echo chat for now
                })
            });

            if (res.ok) {
                const data = await res.json();
                const jobId = data.job_id;

                // 3. Add Placeholder Assistant Message linked to Job ID
                setMessages(prev => [...prev, {
                    id: jobId, // Link this bubble to the job
                    role: 'assistant',
                    text: '', // Empty while thinking
                    status: 'pending',
                    timestamp: Date.now()
                }]);
            }
        } catch (e) {
            console.error(e);
        }
    };

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            sendMessage();
        }
    };

    return (
        <div className="h-[calc(100vh-64px)] flex flex-col p-6 max-w-4xl mx-auto w-full">
            <Card className="flex-1 bg-slate-900 border-slate-800 shadow-xl flex flex-col overflow-hidden">
                {/* Header */}
                <div className="p-4 border-b border-slate-800 bg-slate-950/50 flex items-center gap-3">
                    <div className="p-2 bg-indigo-600/20 rounded-lg text-indigo-400">
                        <Bot size={24} />
                    </div>
                    <div>
                        <h2 className="font-bold text-slate-100">coretex Chat</h2>
                        <div className="flex items-center gap-2">
                             <span className="flex h-2 w-2 rounded-full bg-green-500 animate-pulse"></span>
                             <span className="text-xs text-slate-500 font-mono">job.chat.simple</span>
                        </div>
                    </div>
                </div>

                {/* Messages Area */}
                <div ref={scrollRef} className="flex-1 overflow-y-auto p-6 space-y-6">
                    {messages.length === 0 && (
                        <div className="h-full flex flex-col items-center justify-center text-slate-600 opacity-50">
                            <Sparkles size={48} className="mb-4" />
                            <p>Start a conversation with the Neural Mesh</p>
                        </div>
                    )}

                    {messages.map((msg) => (
                        <div 
                            key={msg.id} 
                            className={clsx(
                                "flex gap-4 max-w-[80%]",
                                msg.role === 'user' ? "ml-auto flex-row-reverse" : ""
                            )}
                        >
                            <div className={clsx(
                                "w-8 h-8 rounded-full flex items-center justify-center shrink-0",
                                msg.role === 'user' ? "bg-slate-700 text-slate-300" : "bg-indigo-600 text-white"
                            )}>
                                {msg.role === 'user' ? <User size={16} /> : <Bot size={16} />}
                            </div>

                            <div className={clsx(
                                "p-4 rounded-2xl text-sm leading-relaxed",
                                msg.role === 'user' 
                                    ? "bg-slate-800 text-slate-200 rounded-tr-sm" 
                                    : "bg-indigo-900/20 text-indigo-100 border border-indigo-500/20 rounded-tl-sm"
                            )}>
                                {msg.status === 'pending' ? (
                                    <div className="flex items-center gap-2 text-indigo-300/50">
                                        <Loader2 size={16} className="animate-spin" />
                                        <span className="text-xs font-mono">Thinking...</span>
                                    </div>
                                ) : (
                                    msg.text
                                )}
                            </div>
                        </div>
                    ))}
                </div>

                {/* Input Area */}
                <div className="p-4 bg-slate-950 border-t border-slate-800">
                    <div className="relative flex items-center gap-2">
                        <input
                            type="text"
                            value={input}
                            onChange={(e) => setInput(e.target.value)}
                            onKeyDown={handleKeyDown}
                            placeholder="Type your message..."
                            className="flex-1 bg-slate-900 border border-slate-800 rounded-xl px-4 py-3 text-slate-200 focus:outline-none focus:ring-2 focus:ring-indigo-500/50 focus:border-indigo-500 transition-all placeholder:text-slate-600"
                            autoFocus
                        />
                        <button 
                            onClick={sendMessage}
                            disabled={!input.trim()}
                            className="p-3 bg-indigo-600 hover:bg-indigo-500 text-white rounded-xl transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <Send size={20} />
                        </button>
                    </div>
                    <div className="text-center mt-2">
                         <span className="text-[10px] text-slate-600">
                            Powered by coretexOS - Latency: &lt;100ms
                        </span>
                    </div>
                </div>
            </Card>
        </div>
    );
};

export default Chat;
