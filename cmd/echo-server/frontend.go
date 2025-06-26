package main

var websocketHTML = `
<html>
    <head>
        <title>websocket</title>
    </head>
    <style>
    #console {
         font-family: monospace;
         font-weight: bold;
         line-height: 1.5em;
         border-top: 1px dashed lightgray;
    }

    #console div {
        border-bottom: 1px dashed lightgray;
    }

    #console div:before {
        display: inline-block;
        width: 5em;
    }

    #console div.send, div.recv {
        font-weight: normal;
        color: gray;
    }

    #console div.info:before {
        color: black;
        content: "[info]";
    }

    #console div.error:before {
        color: red;
        content: "[error]";
    }

    #console div.send:before {
        color: blue;
        content: "[send]";
    }

    #console div.recv:before {
        color: green;
        content: "[recv]";
    }

    .hidden {
        display: none;
    }

    button {
        border-radius: 0.3em;
        border: 1px solid lightgray;
        background: white;
    }

    #msg {
        margin-top: 0.5em;
        text-align: right;
    }

    #msg textarea {
        text-align: left;
        border-radius: 0.3em;
        border: 1px solid lightgray;
        width: 100%;
        display: block;
        min-height: 8em;
        min-width: 20em;
    }

    #msg button {
        margin-top: 0.5em;
    }

    #panel {
        position: fixed;
        top: 1em;
        right: 1em;

        border: 1px solid lightgray;
        border-radius: 0.3em;
        background: white;
        padding: 0.5em;
    }

    #footer {
        margin-top: 1em;
        padding-top: 0.5em;
        border-top: 1px dashed lightgray;
        text-align: center;
        font-size: 0.9em;
        color: #666;
        font-family: Arial, sans-serif;
    }

    #footer a {
        color: #0066cc;
        text-decoration: none;
        font-weight: bold;
    }

    #footer a:hover {
        text-decoration: underline;
    }

    </style>
    <body>
        <div id="panel" />
            <div>
                <button id="pause" class="hidden">Pause Messaging</button>
                <button id="resume" class="hidden">Resume Messaging</button>
                <button id="connect" class="hidden">Connect to Server</button>
                <button id="disconnect" class="hidden">Disconnect from Server</button>
                <button id="cancel" class="hidden">Cancel Connection Attempt</button>
            </div>
            <div id="msg" class="hidden">
                <textarea id="content"></textarea>
                <button id="send">Send Message</button>
            </div>
            <div id="footer">
                Brought to you by <a href="https://websocket.org" target="_blank">WebSocket.org</a>
            </div>
        </div>
        <div id="console" />
        <script>
            var ws
            var messageDelay = 1500
            var connectDelay = 5000
            var autoReconnect = true;

            function log(text, classes) {
                var node = document.createElement("div");
                node.textContent = text;
                node.className = classes
                document.getElementById('console').appendChild(node);
                window.scrollTo(0,document.body.scrollHeight);
            }

            var messageTimer = null
            var connectTimer = null
            var counter = 0
            var lastMessageWasTimeout = false

            function send() {
                var data = counter + ' = 0x' + counter.toString(16);
                ws.send(data);
                log(data, 'send');
                counter++;
                clearTimeout(messageTimer);
                messageTimer = setTimeout(send, messageDelay);
            }

            function connect() {
                log('attempting to connect', 'info')

                autoReconnect = true;
                lastMessageWasTimeout = false; // Reset timeout flag for new connection
                msgPanel.className = 'hidden';
                pauseBtn.className = 'hidden';
                resumeBtn.className = 'hidden';
                connectBtn.className = 'hidden';
                disconnectBtn.className = 'hidden';
                cancelBtn.className = '';

                ws = new WebSocket(
                    location.protocol === 'https:'
                        ? 'wss://' + window.location.host
                        : 'ws://' + window.location.host
                );

                ws.onopen = function (ev) {
                    msgPanel.className = '';
                    pauseBtn.className = '';
                    resumeBtn.className = 'hidden';
                    connectBtn.className = 'hidden';
                    disconnectBtn.className = '';
                    cancelBtn.className = 'hidden';

                    console.log(ev);
                    log('connected', 'info');
                    
                    // Reset timeout flag on new connection
                    lastMessageWasTimeout = false;

                    clearTimeout(messageTimer);
                    messageTimer = setTimeout(send, messageDelay);

                    ws.onclose = function (ev) {
                        console.log('WebSocket close event:', ev);
                        console.log('Close code:', ev.code);
                        console.log('Close reason:', ev.reason);
                        console.log('Was clean:', ev.wasClean);
                        
                        clearTimeout(messageTimer);
                        clearTimeout(connectTimer);

                        // Check if server closed connection due to timeout
                        // Note: Some browsers may not properly receive the close reason
                        var isTimeoutClose = false;
                        
                        // Check multiple conditions for timeout detection
                        if (ev.reason && ev.reason.includes('Connection timeout')) {
                            isTimeoutClose = true;
                        } else if (ev.code === 1000 && ev.wasClean && lastMessageWasTimeout) {
                            // Fallback: if we received a timeout message just before close
                            isTimeoutClose = true;
                        }

                        if (isTimeoutClose || lastMessageWasTimeout) {
                            // Server explicitly closed due to timeout - don't reconnect
                            msgPanel.className = 'hidden';
                            pauseBtn.className = 'hidden';
                            resumeBtn.className = 'hidden';
                            connectBtn.className = '';
                            disconnectBtn.className = 'hidden';
                            cancelBtn.className = 'hidden';

                            if (!lastMessageWasTimeout) {
                                // Only log if we didn't already log in onmessage
                                var timeoutReason = ev.reason || 'Connection closed by server due to timeout';
                                log('SERVER TIMEOUT: ' + timeoutReason, 'error');
                                log('Connection terminated by server - no auto-reconnect', 'error');
                            }
                            log('To reconnect, click the Connect button', 'info');
                            
                            // Reset the flag for next connection
                            lastMessageWasTimeout = false;
                        } else if (autoReconnect) {
                            msgPanel.className = 'hidden';
                            pauseBtn.className = 'hidden';
                            resumeBtn.className = 'hidden';
                            connectBtn.className = 'hidden';
                            disconnectBtn.className = 'hidden';
                            cancelBtn.className = '';

                            log('disconnected, reconnecting in ' + (connectDelay / 1000) + ' seconds', 'info');
                            connectTimer = setTimeout(connect, connectDelay);
                        } else {
                            msgPanel.className = 'hidden';
                            pauseBtn.className = 'hidden';
                            resumeBtn.className = 'hidden';
                            connectBtn.className = '';
                            disconnectBtn.className = 'hidden';
                            cancelBtn.className = 'hidden';

                            log('disconnected', 'info');
                        }
                    }
                    ws.onerror = function (ev) {
                        console.log(ev);
                        log('an error occurred');
                    }
                };
                ws.onmessage = function (ev) {
                    console.log(ev);
                    
                    // Check if this is a timeout message
                    if (ev.data && ev.data.includes && ev.data.includes('Connection timeout')) {
                        lastMessageWasTimeout = true;
                        autoReconnect = false; // Disable auto-reconnect immediately
                        
                        // Log the timeout message prominently
                        log('SERVER TIMEOUT: ' + ev.data, 'error');
                        log('Connection will close - no auto-reconnect', 'error');
                        return; // Don't log as regular message
                    }
                    
                    log(ev.data, 'recv');
                }
                ws.onerror = function (ev) {
                    console.log(ev);
                    clearTimeout(messageTimer);
                    clearTimeout(connectTimer);

                    if (autoReconnect) {
                        msgPanel.className = 'hidden';
                        pauseBtn.className = 'hidden';
                        resumeBtn.className = 'hidden';
                        connectBtn.className = 'hidden';
                        disconnectBtn.className = 'hidden';
                        cancelBtn.className = '';

                        log('unable to connect, retrying in ' + (connectDelay / 1000) + ' seconds', 'error');
                        connectTimer = setTimeout(connect, connectDelay);
                    } else {
                        msgPanel.className = 'hidden';
                        pauseBtn.className = 'hidden';
                        resumeBtn.className = 'hidden';
                        connectBtn.className = '';
                        disconnectBtn.className = 'hidden';
                        cancelBtn.className = 'hidden';

                        log('unable to connect', 'error');
                        log('disconnected', 'info');
                    }
                }
            }

            var pauseBtn = document.getElementById('pause');
            pauseBtn.onclick = function () {
                pauseBtn.className = 'hidden';
                resumeBtn.className = '';
                clearTimeout(messageTimer);
                log('paused messages', 'info');
            }

            var resumeBtn = document.getElementById('resume');
            resumeBtn.onclick = function () {
                pauseBtn.className = '';
                resumeBtn.className = 'hidden';
                log('resumed messages', 'info');
                send();
            }

            var connectBtn = document.getElementById('connect');
            connectBtn.onclick = function () {
                clearTimeout(connectTimer);
                clearTimeout(messageTimer);
                connect();
            }

            var disconnectBtn = document.getElementById('disconnect');
            disconnectBtn.onclick = function () {
                msgPanel.className = 'hidden';
                pauseBtn.className = 'hidden';
                resumeBtn.className = 'hidden';
                connectBtn.className = '';
                cancelBtn.className = 'hidden';
                disconnectBtn.className = 'hidden';

                autoReconnect = false;
                ws.close();
                clearTimeout(connectTimer);
                clearTimeout(messageTimer);
            }

            var cancelBtn = document.getElementById('cancel');
            cancelBtn.onclick = function () {
                msgPanel.className = 'hidden';
                pauseBtn.className = 'hidden';
                resumeBtn.className = 'hidden';
                connectBtn.className = '';
                cancelBtn.className = 'hidden';
                disconnectBtn.className = 'hidden';

                log('cancelled connection attempt', 'info');
                autoReconnect = false;
                clearTimeout(connectTimer);
                clearTimeout(messageTimer);
            }

            var msgPanel = document.getElementById('msg');
            var msgContent = document.getElementById('content');
            var sendBtn = document.getElementById('send');
            sendBtn.onclick = function () {
                ws.send(msgContent.value);
                log(msgContent.value, 'send');
            }

            connect()
        </script>
    </body>
</html>
`
