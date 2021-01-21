import React from 'react'

type Status = 'beta' | 'prototype'

const statusStyleMapping: Record<Status, string> = {
    prototype: 'badge-warning text-white',
    beta: 'badge-info',
}

export const StatusAnnotation: React.FC<{ status: Status }> = ({ status }) => (
    <div className="d-flex align-items-center">
        <span className={`badge ${statusStyleMapping[status]} text-uppercase mr-2`}>{status}</span>
        <a href="mailto:support@sourcegraph.com" target="_blank" rel="noopener noreferrer">
            Share feedback
        </a>
    </div>
)